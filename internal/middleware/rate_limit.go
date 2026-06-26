package middleware

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"

	"aihop.io/ainode/internal/billing"
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

// RateLimiter 检查 RPM (Requests Per Minute) 或 TPM (Tokens Per Minute)
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

// RPMAndTPMMiddleware 创建一个 HTTP 中间件，用于校验用户的并发速率
func RPMAndTPMMiddleware(queries *db.Queries, maxRPM int64, maxTPM int64) func(http.Handler) http.Handler {
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

			estimatedTokens, ok := ctx.Value(reqctx.KeyEstimatedTokens).(int64)
			if !ok || estimatedTokens <= 0 {
				estimatedTokens = 1
			}

			// 退款助手函数
			refundPreDeduction := func() {
				preDeducted, _ := ctx.Value(reqctx.KeyPreDeductedCents).(int64)
				subDeducted, _ := ctx.Value(reqctx.KeySubDeducted).(int64)
				grantDeducted, _ := ctx.Value(reqctx.KeyGrantDeducted).(int64)
				cashDeducted, _ := ctx.Value(reqctx.KeyCashDeducted).(int64)
				reqID, _ := ctx.Value(reqctx.KeyRequestID).(string)
				if preDeducted > 0 {
					billing.Refund(context.Background(), queries, userID, preDeducted,
						billing.Deduction{Sub: subDeducted, Grant: grantDeducted, Cash: cashDeducted}, reqID)
				}
			}

			// 1. 检查 RPM
			rpmKey := fmt.Sprintf("rate:rpm:%d", userID)
			rpmAllowed, err := checkRateLimit(ctx, rpmKey, 60, maxRPM, 1)
			if err != nil {
				refundPreDeduction()
				utils.WriteOpenAIError(w, http.StatusInternalServerError, "Internal Server Error", "server_error", "")
				return
			}
			if !rpmAllowed {
				refundPreDeduction()
				utils.WriteOpenAIError(w, http.StatusTooManyRequests, "RPM limit exceeded", "rate_limit_error", "rpm_exceeded")
				return
			}

			// 2. 检查 TPM
			tpmKey := fmt.Sprintf("rate:tpm:%d", userID)
			tpmAllowed, err := checkRateLimit(ctx, tpmKey, 60, maxTPM, estimatedTokens)
			if err != nil {
				refundPreDeduction()
				utils.WriteOpenAIError(w, http.StatusInternalServerError, "Internal Server Error", "server_error", "")
				return
			}
			if !tpmAllowed {
				refundPreDeduction()
				utils.WriteOpenAIError(w, http.StatusTooManyRequests, "TPM limit exceeded", "rate_limit_error", "tpm_exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
