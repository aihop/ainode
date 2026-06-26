package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"aihop.io/ainode/internal/billing"
	"aihop.io/ainode/internal/config"
	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/reqctx"
	"aihop.io/ainode/internal/utils"
)

const modelConcurrencyKeyTTL = 2 * time.Minute

var errModelConcurrencyExceeded = errors.New("model concurrency exceeded")

func acquireModelConcurrencySlot(ctx context.Context, modelName string, limit int32) (func(), error) {
	if limit <= 0 || billing.RedisClient == nil {
		return func() {}, nil
	}

	key := fmt.Sprintf("concurrency:model:%s", modelName)
	current, err := billing.RedisClient.Incr(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	if err := billing.RedisClient.Expire(ctx, key, modelConcurrencyKeyTTL).Err(); err != nil {
		billing.RedisClient.Decr(context.Background(), key)
		return nil, err
	}

	if current > int64(limit) {
		billing.RedisClient.Decr(context.Background(), key)
		return nil, errModelConcurrencyExceeded
	}

	released := false
	return func() {
		if released || billing.RedisClient == nil {
			return
		}
		released = true

		value, err := billing.RedisClient.Decr(context.Background(), key).Result()
		if err == nil && value <= 0 {
			billing.RedisClient.Del(context.Background(), key)
		}
	}, nil
}

func refundPreDeduction(ctx context.Context, queries *db.Queries, userID int32) {
	preDeducted, _ := ctx.Value(reqctx.KeyPreDeductedCents).(int64)
	grantDeducted, _ := ctx.Value(reqctx.KeyGrantDeducted).(int64)
	cashDeducted, _ := ctx.Value(reqctx.KeyCashDeducted).(int64)
	reqID, _ := ctx.Value(reqctx.KeyRequestID).(string)
	if preDeducted > 0 {
		billing.Refund(context.Background(), queries, userID, preDeducted, grantDeducted, cashDeducted, reqID)
	}
}

// ModelConcurrencyMiddleware 限制单个模型的全局并发数，适合接入上游模型并发配额。
func ModelConcurrencyMiddleware(queries *db.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

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

			modelName, _ := ctx.Value(reqctx.KeyModelName).(string)
			if modelName == "" {
				next.ServeHTTP(w, r)
				return
			}

			modelInfo, err := config.GlobalModelManager.GetModel(ctx, queries, modelName)
			if err != nil {
				refundPreDeduction(ctx, queries, userID)
				utils.WriteOpenAIError(w, http.StatusBadRequest, fmt.Sprintf("Unsupported model: %s", modelName), "invalid_request_error", "unsupported_model")
				return
			}

			release, err := acquireModelConcurrencySlot(ctx, modelName, modelInfo.MaxConcurrency)
			if err != nil {
				refundPreDeduction(ctx, queries, userID)
				if errors.Is(err, errModelConcurrencyExceeded) {
					utils.WriteOpenAIError(w, http.StatusTooManyRequests, "Model concurrency limit exceeded", "rate_limit_error", "model_concurrency_exceeded")
					return
				}

				utils.WriteOpenAIError(w, http.StatusInternalServerError, "Internal Server Error", "server_error", "")
				return
			}
			defer release()

			next.ServeHTTP(w, r)
		})
	}
}
