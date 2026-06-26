package middleware

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"aihop.io/ainode/internal/billing"
	"aihop.io/ainode/internal/config"
	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/media"
	"aihop.io/ainode/internal/reqctx"
	"aihop.io/ainode/internal/utils"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// AuthAndPreDeductMiddleware 负责鉴权、解析请求体、预估 Token 并调用预扣费逻辑
func AuthAndPreDeductMiddleware(queries *db.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			ctx = context.WithValue(ctx, reqctx.KeyRequestStartTime, time.Now())

			// 1. 提取并校验 API Key
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				utils.WriteOpenAIError(w, http.StatusUnauthorized, "Missing or invalid token", "invalid_request_error", "invalid_api_key")
				return
			}
			apiKey := strings.TrimPrefix(authHeader, "Bearer ")

			user, err := queries.GetUserByAPIKey(ctx, apiKey)
			if err != nil {
				utils.WriteOpenAIError(w, http.StatusUnauthorized, "Invalid API Key", "invalid_request_error", "invalid_api_key")
				return
			}
			ctx = context.WithValue(ctx, reqctx.KeyUserID, user.ID)
			ctx = context.WithValue(ctx, reqctx.KeyAPIKeyID, user.KeyID)

			// 如果不是主要的计费接口（比如 /v1/models 或 /v1/dashboard），直接放行
			// 我们主要拦截会产生大量消耗的生成接口
			isChatRoute := strings.HasSuffix(r.URL.Path, "/chat/completions") || strings.HasSuffix(r.URL.Path, "/completions")
			isImageRoute := strings.HasSuffix(r.URL.Path, "/images/generations") || strings.HasSuffix(r.URL.Path, "/image/generations")
			isVideoRoute := strings.HasSuffix(r.URL.Path, "/video/generations")
			isBillingRoute := isChatRoute || isImageRoute || isVideoRoute
			if !isBillingRoute {
				// 标记为非计费请求，跳过预扣费，直接走到限流和代理
				ctx = context.WithValue(ctx, reqctx.KeyIsBillingRoute, false)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			ctx = context.WithValue(ctx, reqctx.KeyIsBillingRoute, true)

			// 2. 解析请求体以获取 Model 和 Prompt
			var (
				modelName       string
				promptTokens    int
				maxOutputTokens int
				requestType     string
				billingUnits    int64 = 1
			)
			if r.Body != nil {
				// 1. OOM 防护：使用 LimitReader 限制最大读取量为 5MB (5 * 1024 * 1024)
				// 防止恶意用户发送超大文件导致内存溢出
				const MaxBodySize = 5 * 1024 * 1024
				limitedBody := io.LimitReader(r.Body, MaxBodySize)

				bodyBytes, readErr := io.ReadAll(limitedBody)
				if readErr != nil {
					utils.WriteOpenAIError(w, http.StatusBadRequest, "Cannot read request body", "invalid_request_error", "")
					return
				}

				// 如果读到的数据刚好等于最大限制，说明实际 Body 更大
				if int64(len(bodyBytes)) == MaxBodySize {
					utils.WriteOpenAIError(w, http.StatusRequestEntityTooLarge, "Request body too large (limit 5MB)", "invalid_request_error", "body_too_large")
					return
				}

				// 恢复 Body
				r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

				switch {
				case isChatRoute:
					requestType = "chat"
					parsedPayload, parseErr := media.ParseChatCompletionRequest(bodyBytes)
					if parseErr != nil {
						utils.WriteOpenAIError(w, http.StatusBadRequest, "Invalid JSON format", "invalid_request_error", "")
						return
					}
					modelName = parsedPayload.Model
					promptTokens = estimateChatPromptTokens(parsedPayload.Model, parsedPayload)
					maxOutputTokens = parsedPayload.MaxTokens
					if maxOutputTokens <= 0 {
						maxOutputTokens = 4096
					}
				case isImageRoute:
					requestType = "image_generation"
					parsedPayload, parseErr := media.ParseImageGenerationRequest(bodyBytes)
					if parseErr != nil {
						utils.WriteOpenAIError(w, http.StatusBadRequest, "Invalid JSON format", "invalid_request_error", "")
						return
					}
					modelName = parsedPayload.Model
					promptTokens = estimatePlainTextTokens(parsedPayload.Model, parsedPayload.Prompt)
					billingUnits = int64(parsedPayload.N)
					maxOutputTokens = 0
				case isVideoRoute:
					requestType = "video_generation"
					parsedPayload, parseErr := media.ParseVideoGenerationRequest(bodyBytes)
					if parseErr != nil {
						utils.WriteOpenAIError(w, http.StatusBadRequest, "Invalid JSON format", "invalid_request_error", "")
						return
					}
					modelName = parsedPayload.Model
					promptTokens = estimatePlainTextTokens(parsedPayload.Model, parsedPayload.Prompt)
					billingUnits = 1
					maxOutputTokens = 0
				}
			}

			if modelName == "" {
				utils.WriteOpenAIError(w, http.StatusBadRequest, "Model is required", "invalid_request_error", "model_required")
				return
			}

			// 3. 获取模型定价并计算最大可能消耗
			modelInfo, err := config.GlobalModelManager.GetModel(ctx, queries, modelName)
			if err != nil {
				log.Printf("Model %s not found in DB, using fallback pricing", modelName)
				logMiddlewareFailure(queries, ctx, http.StatusBadRequest, modelName, "invalid_request_error", "unsupported_model", fmt.Sprintf("Unsupported model: %s", modelName))
				utils.WriteOpenAIError(w, http.StatusBadRequest, fmt.Sprintf("Unsupported model: %s", modelName), "invalid_request_error", "unsupported_model")
				return
			}

			// 3.1 校验模型 modality 是否匹配当前路由类型
			expectedModality := "text"
			switch {
			case isImageRoute:
				expectedModality = "image"
			case isVideoRoute:
				expectedModality = "video"
			case isChatRoute:
				expectedModality = "text" // text 和 vision 都允许走 chat 路由
			}
			if modelInfo.Modality != expectedModality {
				// vision 模型可以走 chat 路由（多模态对话）
				if !(isChatRoute && modelInfo.Modality == "vision") {
					logMiddlewareFailure(queries, ctx, http.StatusBadRequest, modelName, "invalid_request_error", "modality_mismatch",
						fmt.Sprintf("Model %s modality %q does not match route type %q", modelName, modelInfo.Modality, requestType))
					utils.WriteOpenAIError(w, http.StatusBadRequest,
						fmt.Sprintf("Model %q is a %s model and cannot be used on this endpoint", modelName, modelInfo.Modality),
						"invalid_request_error", "modality_mismatch")
					return
				}
			}

			// 计算预扣费：先算基础成本，再应用倍率。预扣费使用向上取整，避免低估。
			estimatedBaseCostCents := int64(0)
			if modelInfo.PricingMode == "request" {
				requestPrice := utils.ParseRequestPricingConfig(modelInfo.PricingConfig).RequestPriceCents
				estimatedBaseCostCents = requestPrice * billingUnits
			} else {
				estimatedBaseCostCents = (int64(promptTokens)*modelInfo.InputPriceCents + int64(maxOutputTokens)*modelInfo.OutputPriceCents) / 1000000
			}
			estimatedCostCents := utils.ApplyMultiplier(estimatedBaseCostCents, modelInfo.Multiplier, true)

			// 确保预扣费至少为 1 分钱，防止余额为 0 时因整数除法结果归零而绕过余额检查
			if estimatedCostCents == 0 {
				estimatedCostCents = 1
			}

			// 4.5 检查 API Key 级配额限制 (Key 生命周期累计消费上限)
			if user.QuotaLimit.Valid && user.QuotaLimit.Int64 > 0 {
				currentUsed := int64(0)
				if user.QuotaUsed.Valid {
					currentUsed = user.QuotaUsed.Int64
				}
				if currentUsed+estimatedCostCents > user.QuotaLimit.Int64 {
					logMiddlewareFailure(queries, ctx, http.StatusPaymentRequired, modelName, "billing_error", "key_quota_exceeded",
						fmt.Sprintf("API Key quota exceeded: used %d + estimated %d > limit %d cents", currentUsed, estimatedCostCents, user.QuotaLimit.Int64))
					utils.WriteOpenAIError(w, http.StatusPaymentRequired, "API Key quota exceeded", "invalid_request_error", "insufficient_quota")
					return
				}
			}

			// 5. 调用预扣费 (如果余额不足会返回错误)
			deduction, err := billing.PreDeduct(ctx, queries, user.ID, estimatedCostCents, modelInfo.BillingPolicy)
			if err != nil {
				logMiddlewareFailure(queries, ctx, http.StatusPaymentRequired, modelName, "billing_error", "insufficient_quota", "Insufficient balance")
				utils.WriteOpenAIError(w, http.StatusPaymentRequired, "Insufficient balance", "invalid_request_error", "insufficient_quota")
				return
			}

			reqID := uuid.New().String()

			// 6. 将核心数据注入 Context 供后续中间件 (限流) 和 Proxy 结算使用
			ctx = context.WithValue(ctx, reqctx.KeyModelName, modelName)
			ctx = context.WithValue(ctx, reqctx.KeyPublicModelName, modelName)
			ctx = context.WithValue(ctx, reqctx.KeyUpstreamModelName, modelName)
			ctx = context.WithValue(ctx, reqctx.KeyRequestType, requestType)
			ctx = context.WithValue(ctx, reqctx.KeyPromptTokens, promptTokens)
			ctx = context.WithValue(ctx, reqctx.KeyPreDeductedCents, estimatedCostCents)
			ctx = context.WithValue(ctx, reqctx.KeySubPaidDeducted, deduction.SubPaid)
			ctx = context.WithValue(ctx, reqctx.KeyGrantDeducted, deduction.Grant)
			ctx = context.WithValue(ctx, reqctx.KeyCashDeducted, deduction.Cash)
			ctx = context.WithValue(ctx, reqctx.KeyBillingUnits, billingUnits)
			ctx = context.WithValue(ctx, reqctx.KeyEstimatedTokens, int64(promptTokens+maxOutputTokens)) // 给 TPM 限流用
			ctx = context.WithValue(ctx, reqctx.KeyRequestID, reqID)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// estimatePromptTokens 粗略估算输入的 Token 数量
func estimateChatPromptTokens(model string, payload *media.ChatCompletionRequest) int {
	tkm := utils.GetTokenizer(model)
	if tkm == nil {
		return 10 // 极度兜底
	}

	return media.EstimatePromptTokens(payload, func(value string) int {
		return len(tkm.Encode(value, nil, nil))
	})
}

func estimatePlainTextTokens(model, text string) int {
	tkm := utils.GetTokenizer(model)
	if tkm == nil {
		return 10
	}

	return len(tkm.Encode(text, nil, nil))
}

// logMiddlewareFailure 在鉴权/预扣费阶段将用户侧失败记录到 model_failure_logs
func logMiddlewareFailure(queries *db.Queries, ctx context.Context, statusCode int, modelName, errorType, errorCode, errorMessage string) {
	if queries == nil || statusCode <= 0 {
		return
	}

	userID, _ := ctx.Value(reqctx.KeyUserID).(int32)
	if userID <= 0 {
		return
	}

	apiKeyID, _ := ctx.Value(reqctx.KeyAPIKeyID).(int32)

	latencyMs := int32(0)
	if startedAt, ok := ctx.Value(reqctx.KeyRequestStartTime).(time.Time); ok {
		latencyMs = int32(time.Since(startedAt).Milliseconds())
	}

	params := db.CreateModelFailureLogParams{
		UserID:       userID,
		ApiKeyID:     pgtype.Int4{Int32: apiKeyID, Valid: apiKeyID > 0},
		ModelName:    modelName,
		ErrorType:    errorType,
		ErrorCode:    errorCode,
		StatusCode:   int32(statusCode),
		ErrorMessage: errorMessage,
		LatencyMs:    latencyMs,
		IsRetryable:  false,
	}

	_ = queries.CreateModelFailureLog(context.Background(), params)
}
