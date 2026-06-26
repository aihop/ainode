package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"aihop.io/ainode/internal/adapter"
	"aihop.io/ainode/internal/billing"
	"aihop.io/ainode/internal/channel"
	"aihop.io/ainode/internal/config"
	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/metrics"
	"aihop.io/ainode/internal/utils"
)

// FallbackTransport 实现 http.RoundTripper 接口，用于在遇到上游限流或错误时进行重试
type FallbackTransport struct {
	OriginalTransport http.RoundTripper
	MaxRetries        int
	DBQueries         *db.Queries
}

func (t *FallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error
	var reqBodyBytes []byte

	// 缓存请求体以便重试时可以重复发送
	if req.Body != nil {
		reqBodyBytes, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}

	for i := 0; i <= t.MaxRetries; i++ {
		// 每次请求前恢复 Body
		if reqBodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewBuffer(reqBodyBytes))
		}

		// 判断是否是计费路由
		isBillingRoute, _ := req.Context().Value("is_billing_route").(bool)

		var modelName string
		if isBillingRoute {
			// 获取请求中指定的模型
			modelName, _ = req.Context().Value("model_name").(string)
		} else {
			// 非计费路由 (如 /v1/models)，不需要模型精确路由，传空字符串
			modelName = ""
		}

		// 获取下一个支持该模型的可用渠道
		ch, errChan := channel.GlobalManager.GetNextChannel(modelName)
		if errChan != nil {
			return nil, fmt.Errorf("no available channels for model %s: %w", modelName, errChan)
		}

		// 执行 Adapter 请求体与路由转换 (只有计费路由才需要复杂的转换)
		if isBillingRoute {
			if strings.HasSuffix(req.URL.Path, "/chat/completions") || strings.HasSuffix(req.URL.Path, "/completions") {
				provAdapter := adapter.GetAdapter(ch.Provider)
				if rewriteErr := provAdapter.RewriteRequest(req, modelName); rewriteErr != nil {
					return nil, fmt.Errorf("failed to rewrite request via adapter: %w", rewriteErr)
				}
			}
		}

		// 动态重写请求的 URL 和 Authorization 头部
		upstreamURL, _ := url.Parse(ch.BaseUrl)
		req.URL.Scheme = upstreamURL.Scheme
		req.URL.Host = upstreamURL.Host
		req.Host = upstreamURL.Host

		// 关键修复：正确拼接 BaseUrl 的 Path 和客户端请求的 Path
		// 如果 BaseUrl 是 https://dashscope.aliyuncs.com/compatible-mode/v1
		// 客户端请求是 /v1/chat/completions
		// 这里需要把它们合并（注意去重 /v1，通常 BaseUrl 配置为 https://dashscope.aliyuncs.com/compatible-mode）
		// 为了最大的兼容性，我们可以直接将客户端的原始 Path 附加到 upstreamURL.Path 后面，并清理多余的斜杠
		originalPath := req.URL.Path
		// 如果 BaseUrl 配置了 /v1，而原始请求也是 /v1 开头，我们需要避免拼成 /v1/v1
		if strings.HasSuffix(upstreamURL.Path, "/v1") && strings.HasPrefix(originalPath, "/v1") {
			req.URL.Path = upstreamURL.Path + strings.TrimPrefix(originalPath, "/v1")
		} else {
			req.URL.Path = strings.TrimSuffix(upstreamURL.Path, "/") + originalPath
		}

		req.Header.Set("Authorization", "Bearer "+ch.ApiKey)

		// 将当前选中的 channel ID 和 Provider 注入到请求的 Context 中
		ctx := context.WithValue(req.Context(), "current_channel_id", ch.ID)
		ctx = context.WithValue(ctx, "current_provider", ch.Provider)
		req = req.WithContext(ctx)

		// 发起实际请求
		resp, err = t.OriginalTransport.RoundTrip(req)

		// 判断是否需要重试
		if err != nil || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			log.Printf("Attempt %d: Upstream request to %s failed. Error: %v, Status: %d", i+1, req.URL.String(), err, getStatus(resp))

			// 如果有响应，必须关闭原先的 Body 防止泄露
			if resp != nil && resp.Body != nil {
				resp.Body.Close()
			}

			// 可以选择标记该渠道为故障
			// channel.GlobalManager.MarkChannelFailed(ch.ID)

			continue // 继续下一次重试
		}

		// 成功则直接返回
		break
	}

	return resp, err
}

func getStatus(r *http.Response) int {
	if r == nil {
		return 0
	}
	return r.StatusCode
}

// NewGatewayProxy 创建核心的的反向代理
func NewGatewayProxy(queries *db.Queries) *httputil.ReverseProxy {
	director := func(req *http.Request) {
		// Director 中的 URL 重写逻辑已经移到了 FallbackTransport 中以支持动态重试
		// 这里只做一些基础的头部清理
		req.Header.Del("X-Forwarded-For")

		// 为了精准计费，我们需要告诉上游流式返回时带上 usage 统计 (OpenAI format)
		if reqBodyBytes, err := io.ReadAll(req.Body); err == nil {
			bodyStr := string(reqBodyBytes)
			if strings.Contains(bodyStr, `"stream":true`) || strings.Contains(bodyStr, `"stream": true`) {
				if !strings.Contains(bodyStr, `"stream_options"`) {
					// 注入 stream_options: {"include_usage": true} (简化版，实际应用建议用 json.Unmarshal 处理)
					// 这里仅作为提示，实际的修改需要完整的 JSON 解析和重组
				}
			}
			req.Body = io.NopCloser(bytes.NewBuffer(reqBodyBytes))
		}
	}

	modifyResponse := func(resp *http.Response) error {
		ctx := resp.Request.Context()

		isBillingRoute, _ := ctx.Value("is_billing_route").(bool)
		if !isBillingRoute {
			// 非计费接口直接透传返回，不需要结算和拦截
			return nil
		}

		// 从上下文中提取必要的信息用于结算 (这些通常在进入 Proxy 前的中间件中解析并塞入)
		userID, _ := ctx.Value("user_id").(int32)
		channelID, _ := ctx.Value("current_channel_id").(int32)
		modelName, _ := ctx.Value("model_name").(string)
		reqID, _ := ctx.Value("request_id").(string)
		preDeductedCents, _ := ctx.Value("pre_deducted_cents").(int64)
		grantDeducted, _ := ctx.Value("grant_deducted").(int64)
		cashDeducted, _ := ctx.Value("cash_deducted").(int64)
		promptTokens, _ := ctx.Value("prompt_tokens").(int)
		requestType, _ := ctx.Value("request_type").(string)
		billingUnits, _ := ctx.Value("billing_units").(int64)

		if resp.StatusCode != http.StatusOK {
			// 如果最终还是失败，应该在这里触发全额退还预扣费的回调
			if preDeductedCents > 0 {
				log.Printf("Upstream error %d from channel %d, refunding...", resp.StatusCode, channelID)
				billing.Refund(context.Background(), queries, userID, preDeductedCents, grantDeducted, cashDeducted, reqID)
			}
			// 监控埋点：记录失败请求
			metrics.GatewayRequestTotal.WithLabelValues(modelName, strconv.Itoa(int(channelID)), strconv.Itoa(resp.StatusCode)).Inc()
			return nil
		}

		// 检查是否是流式响应
		isStream := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")

		// 如果无法获取到模型价格，使用兜底值
		inputPrice := int64(0)
		outputPrice := int64(0)
		cacheHitPrice := int64(0)
		cacheMissPrice := int64(0)
		multiplier := float32(1)
		pricingMode := "token"
		requestPrice := int64(0)
		if modelInfo, err := config.GlobalModelManager.GetModel(context.Background(), queries, modelName); err == nil {
			inputPrice = modelInfo.InputPriceCents
			outputPrice = modelInfo.OutputPriceCents
			cacheHitPrice = modelInfo.CacheHitPriceCents
			cacheMissPrice = modelInfo.CacheMissPriceCents
			multiplier = modelInfo.Multiplier
			pricingMode = modelInfo.PricingMode
			requestPrice = utils.ParseRequestPricingConfig(modelInfo.PricingConfig).RequestPriceCents
		}

		onComplete := func(pTokens, cTokens, cacheHitTokens, cacheMissTokens int) {
			// 监控埋点：记录成功请求、耗时及 Token 消耗
			if startTimeObj, ok := ctx.Value("request_start_time").(time.Time); ok {
				duration := time.Since(startTimeObj).Seconds()
				metrics.GatewayRequestDuration.WithLabelValues(modelName, strconv.Itoa(int(channelID))).Observe(duration)
			}
			metrics.GatewayRequestTotal.WithLabelValues(modelName, strconv.Itoa(int(channelID)), "200").Inc()
			metrics.GatewayTokensTotal.WithLabelValues(modelName, strconv.Itoa(int(channelID)), "prompt").Add(float64(pTokens))
			metrics.GatewayTokensTotal.WithLabelValues(modelName, strconv.Itoa(int(channelID)), "completion").Add(float64(cTokens))
			metrics.GatewayTokensTotal.WithLabelValues(modelName, strconv.Itoa(int(channelID)), "cache_hit").Add(float64(cacheHitTokens))
			metrics.GatewayTokensTotal.WithLabelValues(modelName, strconv.Itoa(int(channelID)), "cache_miss").Add(float64(cacheMissTokens))

			// 如果没有解析到缓存 token，默认将所有 prompt_tokens 视为未命中
			if cacheHitTokens == 0 && cacheMissTokens == 0 {
				cacheMissTokens = pTokens
			}

			// 计算实际消耗金额：(缓存命中 * 命中单价 + 缓存未命中 * 未命中单价 + 输出 * 输出单价) / 100万
			// 这里我们假设 promptTokens 总是等于 hit + miss。如果有额外的基础 token，可以加上。
			regularPromptTokens := pTokens - cacheHitTokens - cacheMissTokens
			if regularPromptTokens < 0 {
				regularPromptTokens = 0
			}

			actualBaseCost := int64(0)
			if pricingMode == "request" || requestType == "image_generation" {
				if billingUnits <= 0 {
					billingUnits = 1
				}
				actualBaseCost = requestPrice * billingUnits
			} else {
				actualBaseCost = (int64(regularPromptTokens)*inputPrice +
					int64(cacheHitTokens)*cacheHitPrice +
					int64(cacheMissTokens)*cacheMissPrice +
					int64(cTokens)*outputPrice) / 1000000
			}
			actualCost := utils.ApplyMultiplier(actualBaseCost, multiplier, false)

			settleReq := billing.SettlementRequest{
				UserID:           userID,
				ChannelID:        channelID,
				ModelName:        modelName,
				PromptTokens:     int32(pTokens),
				CompletionTokens: int32(cTokens),
				CacheHitTokens:   int32(cacheHitTokens),
				CacheMissTokens:  int32(cacheMissTokens),
				PreDeductedCents: preDeductedCents,
				GrantDeducted:    grantDeducted,
				CashDeducted:     cashDeducted,
				ActualCostCents:  actualCost,
				RequestID:        reqID,
			}

			// 触发异步结算
			billing.Settle(context.Background(), queries, settleReq)
		}

		if isStream {
			// 从上下文获取 provider
			provider, _ := ctx.Value("current_provider").(string)
			// 挂载 TallyReader 拦截器
			resp.Body = NewTallyReader(resp.Body, modelName, promptTokens, provider, onComplete)
		} else {
			// 非流式响应处理：读取所有 Body 并解析 usage
			bodyBytes, err := io.ReadAll(resp.Body)
			if err == nil {
				// 恢复 Body
				resp.Body.Close()
				resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

				var payload struct {
					Usage *struct {
						PromptTokens        int `json:"prompt_tokens"`
						CompletionTokens    int `json:"completion_tokens"`
						PromptTokensDetails *struct {
							CachedTokens int `json:"cached_tokens"`
						} `json:"prompt_tokens_details"`
						CacheHitTokens  int `json:"cache_hit_tokens"`
						CacheMissTokens int `json:"cache_miss_tokens"`
					} `json:"usage"`
				}

				cTokens := 0
				chTokens := 0
				cmTokens := 0
				pTokens := promptTokens

				if err := json.Unmarshal(bodyBytes, &payload); err == nil && payload.Usage != nil {
					u := payload.Usage
					cTokens = u.CompletionTokens
					if u.PromptTokens > 0 {
						pTokens = u.PromptTokens
					}

					if u.CacheHitTokens > 0 || u.CacheMissTokens > 0 {
						chTokens = u.CacheHitTokens
						cmTokens = u.CacheMissTokens
					} else if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
						chTokens = u.PromptTokensDetails.CachedTokens
						cmTokens = pTokens - chTokens
					}
				}

				// 立即触发结算
				onComplete(pTokens, cTokens, chTokens, cmTokens)
			}
		}

		return nil
	}

	// 监控埋点：记录请求开始时间
	// startTime := time.Now() // 已经移到外层处理，这里不需要了

	return &httputil.ReverseProxy{
		Director:       director,
		ModifyResponse: modifyResponse,
		Transport: &FallbackTransport{
			OriginalTransport: http.DefaultTransport,
			MaxRetries:        2, // 失败最多重试2次下一个渠道
			DBQueries:         queries,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("Proxy Error: %v", err)

			// 在代理发生错误（比如重试所有渠道都失败）时，退还预扣费
			ctx := r.Context()
			isBillingRoute, _ := ctx.Value("is_billing_route").(bool)
			if isBillingRoute {
				userID, _ := ctx.Value("user_id").(int32)
				preDeductedCents, _ := ctx.Value("pre_deducted_cents").(int64)
				grantDeducted, _ := ctx.Value("grant_deducted").(int64)
				cashDeducted, _ := ctx.Value("cash_deducted").(int64)
				reqID, _ := ctx.Value("request_id").(string)
				if preDeductedCents > 0 {
					billing.Refund(context.Background(), queries, userID, preDeductedCents, grantDeducted, cashDeducted, reqID)
				}
			}

			// 记录网关层面的错误 (502 Bad Gateway)
			modelName, _ := ctx.Value("model_name").(string)
			channelID, _ := ctx.Value("current_channel_id").(int32)
			if modelName != "" {
				metrics.GatewayRequestTotal.WithLabelValues(modelName, strconv.Itoa(int(channelID)), "502").Inc()
			}

			// 构建详细的错误响应，包含具体原因
			errMsg := "Bad Gateway: Upstream connection failed"
			if err != nil {
				errMsg = fmt.Sprintf("Bad Gateway: %v", err)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, `{"error":{"message":%q,"type":"server_error","code":"bad_gateway"}}`, errMsg)
		},
	}
}
