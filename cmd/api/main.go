package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"aihop.io/ainode/internal/api/admin"
	"aihop.io/ainode/internal/api/gateway"
	"aihop.io/ainode/internal/api/site"
	"aihop.io/ainode/internal/api/webhook"
	"aihop.io/ainode/internal/billing"
	"aihop.io/ainode/internal/channel"
	"aihop.io/ainode/internal/config"
	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/middleware"
	_ "aihop.io/ainode/internal/provider/aimlapi"
	_ "aihop.io/ainode/internal/provider/anthropic"
	_ "aihop.io/ainode/internal/provider/cohere"
	_ "aihop.io/ainode/internal/provider/deepinfra"
	_ "aihop.io/ainode/internal/provider/deepseek"
	_ "aihop.io/ainode/internal/provider/fireworks"
	_ "aihop.io/ainode/internal/provider/gemini"
	_ "aihop.io/ainode/internal/provider/grok"
	_ "aihop.io/ainode/internal/provider/groq"
	_ "aihop.io/ainode/internal/provider/ideogram"
	_ "aihop.io/ainode/internal/provider/mistral"
	_ "aihop.io/ainode/internal/provider/openai"
	_ "aihop.io/ainode/internal/provider/openrouter"
	_ "aihop.io/ainode/internal/provider/perplexity"
	_ "aihop.io/ainode/internal/provider/qwen"
	_ "aihop.io/ainode/internal/provider/together"
	"aihop.io/ainode/internal/proxy"
	"aihop.io/ainode/internal/worker"
)

func main() {
	// 1. 加载配置
	config.LoadConfig()
	cfg := config.AppConfig

	// 2. 初始化 Redis
	if err := billing.InitRedis(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB); err != nil {
		log.Fatalf("Failed to initialize Redis: %v", err)
	}

	// 3. 初始化 PostgreSQL（显式配置连接池，避免默认 max(4,CPU) 在并发多查询/Worker 场景被打满）
	ctx := context.Background()
	poolCfg, err := pgxpool.ParseConfig(cfg.DB.DSN)
	if err != nil {
		log.Fatalf("Invalid database DSN: %v", err)
	}
	maxConns := cfg.DB.MaxConns
	if maxConns <= 0 {
		maxConns = 20
	}
	minConns := cfg.DB.MinConns
	if minConns < 0 {
		minConns = 0
	}
	if minConns > maxConns {
		minConns = maxConns
	}
	poolCfg.MaxConns = maxConns
	poolCfg.MinConns = minConns
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v", err)
	}
	defer pool.Close()
	log.Printf("PostgreSQL pool: MaxConns=%d, MinConns=%d", maxConns, minConns)

	// 3.1 自动执行 schema.sql 建表
	schemaBytes, err := os.ReadFile("schema.sql")
	if err == nil {
		_, err = pool.Exec(ctx, string(schemaBytes))
		if err != nil {
			log.Printf("Warning: Auto-migration from schema.sql failed: %v", err)
		} else {
			log.Println("Auto-migration successful")
		}
	} else {
		log.Printf("Warning: Could not read schema.sql: %v", err)
	}

	// 3.2 确保 billing_logs 分区表已创建（当前月 + 未来 6 个月）
	// 硬编码分区在 schema.sql 中已移除，改为启动时动态创建 + 定时维护
	if err := billing.EnsureBillingLogPartitions(ctx, pool, billing.EnsurePartitionsMonthsAhead); err != nil {
		log.Printf("Warning: Failed to ensure billing_logs partitions: %v", err)
	} else {
		log.Println("Billing_logs partitions verified")
	}

	queries := db.New(pool)

	// 4. 初始化内存缓存管理器
	channel.InitManager()
	config.InitModelManager()

	// 启动后台定时同步任务 (每5分钟从 DB 拉取最新配置和渠道)
	syncCtx, cancelSync := context.WithCancel(context.Background())
	defer cancelSync()
	config.StartBackgroundSync(syncCtx, queries, 5*time.Minute)

	// 启动分区定时维护任务 (每天检查并创建未来月份的分区)
	billing.StartPartitionMaintenance(syncCtx, pool)

	// 启动结算 outbox relay：兜底重投因 asynq/Redis 故障而落库的结算，确保账单不丢
	billing.StartOutboxRelay(syncCtx, queries)

	// 5. 初始化并启动 Asynq Worker (后台任务消费者)
	srvAsynq := asynq.NewServer(
		asynq.RedisClientOpt{
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		},
		asynq.Config{
			// 并发处理的任务数 (根据你的数据库连接池和机器性能调整)
			Concurrency: 10,
			// 队列优先级
			Queues: map[string]int{
				"ainode_billing": 1,
			},
		},
	)

	mux := asynq.NewServeMux()
	billingProcessor := worker.NewBillingTaskProcessor(queries, pool)
	mux.HandleFunc(billing.TaskRecordBillingLog, billingProcessor.HandleRecordBillingLog)

	go func() {
		log.Println("🚀 Asynq Worker is starting...")
		if err := srvAsynq.Run(mux); err != nil {
			log.Fatalf("Could not start Asynq worker: %v", err)
		}
	}()

	// 6. 设置路由与中间件
	r := chi.NewRouter()

	// 基础中间件
	r.Use(chiMiddleware.RequestID)
	r.Use(chiMiddleware.RealIP)
	r.Use(chiMiddleware.Logger)
	r.Use(chiMiddleware.Recoverer)

	// 配置 CORS
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders: []string{"Link"},
		// 本网关用 Authorization: Bearer 鉴权，不依赖 cookie。
		// AllowedOrigins:"*" 与 AllowCredentials:true 同时设置是反模式（浏览器也会拒绝），
		// 故关闭 credentials，避免误导与潜在的跨站凭证泄露。
		AllowCredentials: false,
		MaxAge:           300, // Maximum value not ignored by any of major browsers
	}))

	// 限流阈值：来自配置（默认已调高），<=0 时回落到安全默认，避免把所有请求挡死
	rpmLimit := cfg.Server.RPMLimit
	if rpmLimit <= 0 {
		rpmLimit = 600
	}
	tpmLimit := cfg.Server.TPMLimit
	if tpmLimit <= 0 {
		tpmLimit = 2000000
	}
	log.Printf("Rate limits per user (60s window): RPM=%d, TPM=%d", rpmLimit, tpmLimit)

	// 核心业务路由组
	r.Group(func(r chi.Router) {
		// ==========================
		// 1. Admin API 路由组 (需鉴权)
		// ==========================
		adminToken := config.AppConfig.Internal.Token
		r.Group(func(adminRouter chi.Router) {
			// 服务间鉴权：校验 Internal Token（由 APayShop 服务端携带）
			adminRouter.Use(middleware.InternalTokenAuth(adminToken))

			adminHandler := admin.NewAdminHandler(queries, pool)

			// Channels
			adminRouter.Get("/api/admin/providers", adminHandler.ListProviders)
			adminRouter.Get("/api/admin/channels", adminHandler.ListChannels)
			adminRouter.Get("/api/admin/channels/health", adminHandler.ListChannelHealth)
			adminRouter.Get("/api/admin/channels/{id}/failures", adminHandler.ListChannelFailureLogs)
			adminRouter.Post("/api/admin/channels/{id}/reset-circuit", adminHandler.ResetChannelCircuitBreaker)
			adminRouter.Post("/api/admin/channels/{id}/probe", adminHandler.ProbeChannel)
			adminRouter.Post("/api/admin/channels", adminHandler.CreateChannel)
			adminRouter.Put("/api/admin/channels/{id}", adminHandler.UpdateChannel)
			adminRouter.Delete("/api/admin/channels/{id}", adminHandler.DeleteChannel)

			// Models
			adminRouter.Get("/api/admin/models", adminHandler.ListModels)
			adminRouter.Post("/api/admin/models", adminHandler.CreateModel)
			adminRouter.Put("/api/admin/models/{model_name}", adminHandler.UpdateModel)
			adminRouter.Delete("/api/admin/models/{model_name}", adminHandler.DeleteModel)

			// Billing Logs
			adminRouter.Get("/api/admin/billing_logs", adminHandler.ListBillingLogs)

			// Users
			adminRouter.Get("/api/admin/users", adminHandler.ListUsers)
			adminRouter.Get("/api/admin/users/summary", adminHandler.UsersSummary)
			adminRouter.Get("/api/admin/users/{id}/balance-logs", adminHandler.ListUserBalanceLogs)
			adminRouter.Post("/api/admin/users/{id}/balance", adminHandler.AdjustUserBalance)
		})

		// Initialize internal handler
		siteHandler := site.NewInternalHandler(queries)
		webhookHandler := webhook.NewHandler(queries, pool)

		// Site API 组 (供 APayShop Node.js 服务端调用)
		r.Group(func(siteRouter chi.Router) {
			// 服务间鉴权：校验 Internal Token（由 APayShop 服务端携带）
			siteRouter.Use(middleware.InternalTokenAuth(adminToken))

			siteRouter.Get("/api/site/wallet", siteHandler.WalletHandler)
			siteRouter.Get("/api/site/stats", siteHandler.StatsHandler)
			siteRouter.Get("/api/site/dashboard", siteHandler.DashboardHandler)
			siteRouter.Get("/api/site/billing-logs/list", siteHandler.BillingLogsListHandler)
			siteRouter.Get("/api/site/balance-logs/list", siteHandler.BalanceLogsListHandler)
			siteRouter.Get("/api/site/model-failure-logs/list", siteHandler.ModelFailureLogsListHandler)
			siteRouter.Get("/api/site/api-keys/list", siteHandler.ListAPIKeysHandler)
			siteRouter.Post("/api/site/api-keys/create", siteHandler.CreateAPIKeyHandler)
			siteRouter.Post("/api/site/api-keys/delete", siteHandler.DeleteAPIKeyHandler)
			siteRouter.Post("/api/site/api-keys/status", siteHandler.UpdateAPIKeyStatusHandler)
			siteRouter.Post("/api/site/api-keys/name", siteHandler.UpdateAPIKeyNameHandler)
			siteRouter.Post("/api/site/api-keys/rotate", siteHandler.RotateAPIKeyHandler)
			siteRouter.Get("/api/site/models/groups", siteHandler.ListModelGroupsHandler)
		})

		// Webhook 自带 Bearer/HMAC 鉴权，不挂 InternalTokenAuth 中间件。
		// 新路径 /api/webhooks/events 对齐 /api/* 命名；旧 /internal/... 过渡期保留，
		// 待 APayShop 切到新地址、确认到账正常后再删除旧路径。
		r.Post("/api/webhooks/events", webhookHandler.HandleEvent)

		// ==========================
		// 2. 异步媒体任务路由组
		// ==========================
		r.Group(func(asyncRouter chi.Router) {
			asyncRouter.Use(middleware.AuthAndPreDeductMiddleware(queries))
			asyncRouter.Use(middleware.RPMAndTPMMiddleware(queries, rpmLimit, tpmLimit))
			asyncRouter.Use(middleware.ModelConcurrencyMiddleware(queries))

			gatewayHandler := gateway.NewGatewayHandler(queries)
			asyncRouter.Post("/v1/video/generations", gatewayHandler.CreateVideoGenerationTask)
			asyncRouter.Get("/v1/tasks/{task_id}", gatewayHandler.GetTask)
			asyncRouter.Post("/v1/tasks/{task_id}/cancel", gatewayHandler.CancelTask)
		})

		// ==========================
		// 3. OpenAI 兼容代理路由组
		// ==========================
		r.Group(func(proxyRouter chi.Router) {
			// A. 鉴权、请求解析与预扣费中间件
			proxyRouter.Use(middleware.AuthAndPreDeductMiddleware(queries))

			// B. RPM 与 TPM 限流中间件 (例如: 每分钟 60 次请求，每分钟 100,000 Token)
			proxyRouter.Use(middleware.RPMAndTPMMiddleware(queries, rpmLimit, tpmLimit))

			// C. 模型级并发限制，按 models.max_concurrency 控制单模型的全局并发占位
			proxyRouter.Use(middleware.ModelConcurrencyMiddleware(queries))

			// D. 获取可用模型列表 (拦截 /v1/models，直接返回内部数据)
			proxyRouter.Get("/v1/models", func(w http.ResponseWriter, r *http.Request) {
				models := config.GlobalModelManager.ListAllModels()

				type ModelObj struct {
					ID      string `json:"id"`
					Object  string `json:"object"`
					Created int64  `json:"created"`
					OwnedBy string `json:"owned_by"`
				}
				var data []ModelObj
				for _, m := range models {
					data = append(data, ModelObj{
						ID:      m.ModelName,
						Object:  "model",
						Created: time.Now().Unix(),
						OwnedBy: "AINode",
					})
				}

				resp := map[string]any{
					"object": "list",
					"data":   data,
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			})

			// E. 挂载代理引擎，接管所有其他请求
			gatewayProxy := proxy.NewGatewayProxy(queries)
			proxyRouter.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
				gatewayProxy.ServeHTTP(w, r)
			})
		})
	})

	// 健康检查
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Prometheus 监控指标暴露接口
	r.Handle("/metrics", promhttp.Handler())

	// 6. 启动服务器与优雅启停
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: r,
	}

	go func() {
		log.Printf("🚀 AINode AI Gateway is running on port %d", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server listen failed: %v", err)
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// 给予最多 10 秒时间完成正在处理的请求和异步结算
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	// 优雅关闭 Asynq Worker
	srvAsynq.Shutdown()

	log.Println("Server exiting")
}
