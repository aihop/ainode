package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"aihop.io/ainode/internal/billing"
	"aihop.io/ainode/internal/config"
	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/utils"

	"github.com/google/uuid"
	"github.com/pkoukk/tiktoken-go"
)

// RequestPayload 用于解析客户端发来的 OpenAI 格式请求
// 注意：为了透传插件等未知参数，我们使用 json.RawMessage 来保留原貌
type RequestPayload struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	MaxTokens int `json:"max_tokens"`
}

// AuthAndPreDeductMiddleware 负责鉴权、解析请求体、预估 Token 并调用预扣费逻辑
func AuthAndPreDeductMiddleware(queries *db.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

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
			ctx = context.WithValue(ctx, "user_id", user.ID)

			// 如果不是主要的计费接口（比如 /v1/models 或 /v1/dashboard），直接放行
			// 我们主要拦截会产生大量消耗的生成接口
			isBillingRoute := strings.HasSuffix(r.URL.Path, "/chat/completions") || strings.HasSuffix(r.URL.Path, "/completions")
			if !isBillingRoute {
				// 标记为非计费请求，跳过预扣费，直接走到限流和代理
				ctx = context.WithValue(ctx, "is_billing_route", false)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			ctx = context.WithValue(ctx, "is_billing_route", true)

			// 2. 解析请求体以获取 Model 和 Prompt
			var payload RequestPayload
			if r.Body != nil {
				// 1. OOM 防护：使用 LimitReader 限制最大读取量为 5MB (5 * 1024 * 1024)
				// 防止恶意用户发送超大文件导致内存溢出
				const MaxBodySize = 5 * 1024 * 1024
				limitedBody := io.LimitReader(r.Body, MaxBodySize)

				bodyBytes, err := io.ReadAll(limitedBody)
				if err != nil {
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

				if err := json.Unmarshal(bodyBytes, &payload); err != nil {
					utils.WriteOpenAIError(w, http.StatusBadRequest, "Invalid JSON format", "invalid_request_error", "")
					return
				}
			}

			if payload.Model == "" {
				utils.WriteOpenAIError(w, http.StatusBadRequest, "Model is required", "invalid_request_error", "model_required")
				return
			}

			// 3. 预估 Prompt Tokens
			promptTokens := estimatePromptTokens(payload.Model, payload)

			// 4. 获取模型定价并计算最大可能消耗
			modelInfo, err := config.GlobalModelManager.GetModel(ctx, queries, payload.Model)
			if err != nil {
				log.Printf("Model %s not found in DB, using fallback pricing", payload.Model)
				utils.WriteOpenAIError(w, http.StatusBadRequest, fmt.Sprintf("Unsupported model: %s", payload.Model), "invalid_request_error", "unsupported_model")
				return
			}

			// 预估最大输出 Token：如果用户传了 max_tokens 就用用户的，否则使用模型默认上限 (假设 4096)
			maxOutputTokens := payload.MaxTokens
			if maxOutputTokens <= 0 {
				maxOutputTokens = 4096
			}

			// 计算预扣费：先算基础成本，再应用倍率。预扣费使用向上取整，避免低估。
			estimatedBaseCostCents := (int64(promptTokens)*modelInfo.InputPriceCents + int64(maxOutputTokens)*modelInfo.OutputPriceCents) / 1000000
			estimatedCostCents := utils.ApplyMultiplier(estimatedBaseCostCents, modelInfo.Multiplier, true)

			// 5. 调用预扣费 (如果余额不足会返回错误)
			grantDeducted, cashDeducted, err := billing.PreDeduct(ctx, queries, user.ID, estimatedCostCents)
			if err != nil {
				utils.WriteOpenAIError(w, http.StatusPaymentRequired, "Insufficient balance", "invalid_request_error", "insufficient_quota")
				return
			}

			reqID := uuid.New().String()

			// 6. 将核心数据注入 Context 供后续中间件 (限流) 和 Proxy 结算使用
			ctx = context.WithValue(ctx, "model_name", payload.Model)
			ctx = context.WithValue(ctx, "prompt_tokens", promptTokens)
			ctx = context.WithValue(ctx, "pre_deducted_cents", estimatedCostCents)
			ctx = context.WithValue(ctx, "grant_deducted", grantDeducted)
			ctx = context.WithValue(ctx, "cash_deducted", cashDeducted)
			ctx = context.WithValue(ctx, "estimated_tokens", int64(promptTokens+maxOutputTokens)) // 给 TPM 限流用
			ctx = context.WithValue(ctx, "request_id", reqID)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// estimatePromptTokens 粗略估算输入的 Token 数量
func estimatePromptTokens(model string, payload RequestPayload) int {
	tkm, err := tiktoken.EncodingForModel(model)
	if err != nil {
		tkm, _ = tiktoken.GetEncoding("cl100k_base")
	}

	if tkm == nil {
		return 10 // 极度兜底
	}

	tokens := 0
	for _, msg := range payload.Messages {
		// 每条消息基础开销
		tokens += 4
		tokens += len(tkm.Encode(msg.Role, nil, nil))
		tokens += len(tkm.Encode(msg.Content, nil, nil))
	}
	tokens += 2 // 对话级别的基础开销
	return tokens
}
