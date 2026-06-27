package middleware

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"net/http"

	"aihop.io/ainode/internal/billing"
	"aihop.io/ainode/internal/config"
	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/reqctx"
	"aihop.io/ainode/internal/utils"
	"github.com/redis/go-redis/v9"
)

//go:embed lua/rate_limit.lua
var rateLimitLuaScript string

var rateLimitScript *redis.Script

func init() {
	rateLimitScript = redis.NewScript(rateLimitLuaScript)
}

// checkRateLimit 检查某个滑动窗口是否超限。
// key: Redis key；windowSeconds: 窗口大小（秒）；maxAllowed: 上限；increment: 本次增加值。
func checkRateLimit(ctx context.Context, key string, windowSeconds int, maxAllowed int64, increment int64) (bool, error) {
	keys := []string{key}
	args := []interface{}{windowSeconds, maxAllowed, increment}

	result, err := rateLimitScript.Run(ctx, billing.RedisClient, keys, args...).Result()
	if err != nil {
		return false, err
	}

	if result.(int64) == 0 {
		return false, nil // 被限流
	}
	return true, nil
}

// RPMAndTPMMiddleware 创建 HTTP 中间件，按用户订阅等级 (tier_level) 差异化限流。
// 限制维度（均为 60s 滚动窗口）：
//   - 用户级 RPM：全局固定阈值或按 tier 差异化
//   - 用户级 TPM：同 RPM
//   - 用户+模型级 RPM：防止单用户打满某模型配额，按 tier 差异化
func RPMAndTPMMiddleware(queries *db.Queries, cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// 如果不是计费路由，不限流
			isBillingRoute, _ := ctx.Value(reqctx.KeyIsBillingRoute).(bool)
			if !isBillingRoute {
				next.ServeHTTP(w, r)
				return
			}

			userID, ok := ctx.Value(reqctx.KeyUserID).(int32)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			// 获取用户订阅等级（未注入则默认 0）
			tierLevel, _ := ctx.Value(reqctx.KeyTierLevel).(int32)
			tierLimit := cfg.Server.ResolveTierLimit(tierLevel)

			estimatedTokens, ok := ctx.Value(reqctx.KeyEstimatedTokens).(int64)
			if !ok || estimatedTokens <= 0 {
				estimatedTokens = 1
			}

			modelName, _ := ctx.Value(reqctx.KeyModelName).(string)

			// 退款助手函数
			refundPreDeduction := func() {
				preDeducted, _ := ctx.Value(reqctx.KeyPreDeductedCents).(int64)
				subDeducted, _ := ctx.Value(reqctx.KeySubDeducted).(int64)
				grantDeducted, _ := ctx.Value(reqctx.KeyGrantDeducted).(int64)
				cashDeducted, _ := ctx.Value(reqctx.KeyCashDeducted).(int64)
				reqID, _ := ctx.Value(reqctx.KeyRequestID).(string)
				apiKeyID, _ := ctx.Value(reqctx.KeyAPIKeyID).(int32)
				channelID, _ := ctx.Value(reqctx.KeyCurrentChannelID).(int32)
				promptTokens, _ := ctx.Value(reqctx.KeyPromptTokens).(int)
				if preDeducted > 0 {
					billing.Refund(context.Background(), queries, userID, preDeducted,
						billing.Deduction{Sub: subDeducted, Grant: grantDeducted, Cash: cashDeducted},
						billing.SettlementRequest{
							UserID:       userID,
							ApiKeyID:     apiKeyID,
							ChannelID:    channelID,
							ModelName:    modelName,
							PromptTokens: int32(promptTokens),
							RequestID:    reqID,
						})
				}
			}

			// ==========================================
			// 1. 检查用户级 RPM
			// ==========================================
			rpmKey := fmt.Sprintf("rate:rpm:%d", userID)
			rpmAllowed, err := checkRateLimit(ctx, rpmKey, 60, tierLimit.RPM, 1)
			if err != nil {
				log.Printf("WARN: RPM rate limit check failed (Redis error): %v, allowing request", err)
			} else if !rpmAllowed {
				refundPreDeduction()
				utils.WriteOpenAIError(w, http.StatusTooManyRequests, "RPM limit exceeded", "rate_limit_error", "rpm_exceeded")
				return
			}

			// ==========================================
			// 2. 检查用户级 TPM
			// ==========================================
			tpmKey := fmt.Sprintf("rate:tpm:%d", userID)
			tpmAllowed, err := checkRateLimit(ctx, tpmKey, 60, tierLimit.TPM, estimatedTokens)
			if err != nil {
				log.Printf("WARN: TPM rate limit check failed (Redis error): %v, allowing request", err)
			} else if !tpmAllowed {
				refundPreDeduction()
				utils.WriteOpenAIError(w, http.StatusTooManyRequests, "TPM limit exceeded", "rate_limit_error", "tpm_exceeded")
				return
			}

			// ==========================================
			// 3. 检查用户+模型级 RPM（如果 tier 配置了 model_rpm > 0）
			// ==========================================
			if tierLimit.ModelRPM > 0 && modelName != "" {
				modelRPMKey := fmt.Sprintf("rate:model_rpm:%d:%s", userID, modelName)
				modelRPMAllowed, err := checkRateLimit(ctx, modelRPMKey, 60, tierLimit.ModelRPM, 1)
				if err != nil {
					log.Printf("WARN: Model RPM rate limit check failed (Redis error): %v, allowing request", err)
				} else if !modelRPMAllowed {
					refundPreDeduction()
					utils.WriteOpenAIError(w, http.StatusTooManyRequests,
						fmt.Sprintf("Model %s RPM limit exceeded", modelName),
						"rate_limit_error", "model_rpm_exceeded")
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
