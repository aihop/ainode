package proxy

import (
	"context"
	"net/http"
	"time"

	"aihop.io/ainode/internal/channel"
	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/provider"
	"aihop.io/ainode/internal/reqctx"

	"github.com/jackc/pgx/v5/pgtype"
)

func truncateString(value string, maxLen int) string {
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen]
}

func classifyModelFailure(statusCode int, translated *provider.ProviderError) (string, string, bool) {
	if translated != nil {
		isRetryable := statusCode == http.StatusTooManyRequests || statusCode >= 500
		if translated.Code == "bad_gateway" {
			isRetryable = true
		}
		return translated.Type, translated.Code, isRetryable
	}

	switch {
	case statusCode == http.StatusTooManyRequests:
		return "rate_limit_error", "rate_limit", true
	case statusCode == http.StatusBadGateway:
		return "server_error", "bad_gateway", true
	case statusCode == http.StatusServiceUnavailable:
		return "server_error", "service_unavailable", true
	case statusCode == http.StatusGatewayTimeout:
		return "server_error", "gateway_timeout", true
	case statusCode >= 500:
		return "server_error", "upstream_server_error", true
	case statusCode == http.StatusUnauthorized:
		return "authentication_error", "unauthorized", false
	case statusCode == http.StatusForbidden:
		return "permission_error", "forbidden", false
	case statusCode == http.StatusPaymentRequired:
		return "billing_error", "insufficient_quota", false
	case statusCode >= 400:
		return "invalid_request_error", "request_failed", false
	default:
		return "server_error", "request_failed", false
	}
}

func logModelFailure(ctx context.Context, queries *db.Queries, statusCode int, responseBody string, translated *provider.ProviderError, fallbackMessage string) {
	if queries == nil {
		return
	}

	userID, ok := ctx.Value(reqctx.KeyUserID).(int32)
	if !ok || userID <= 0 {
		return
	}

	modelName, _ := ctx.Value(reqctx.KeyPublicModelName).(string)
	if modelName == "" {
		modelName, _ = ctx.Value(reqctx.KeyModelName).(string)
	}
	if modelName == "" {
		return
	}

	requestID, _ := ctx.Value(reqctx.KeyRequestID).(string)
	providerName, _ := ctx.Value(reqctx.KeyCurrentProvider).(string)
	apiKeyID, _ := ctx.Value(reqctx.KeyAPIKeyID).(int32)

	latencyMs := int32(0)
	if startedAt, ok := ctx.Value(reqctx.KeyRequestStartTime).(time.Time); ok {
		latencyMs = int32(time.Since(startedAt).Milliseconds())
	}

	errorType, errorCode, isRetryable := classifyModelFailure(statusCode, translated)
	errorMessage := fallbackMessage
	if translated != nil && translated.Message != "" {
		errorMessage = translated.Message
	}
	if errorMessage == "" {
		errorMessage = http.StatusText(statusCode)
	}

	params := db.CreateModelFailureLogParams{
		UserID:       userID,
		ApiKeyID:     pgtype.Int4{Int32: apiKeyID, Valid: apiKeyID > 0},
		RequestID:    truncateString(requestID, 100),
		ModelName:    truncateString(modelName, 100),
		Provider:     truncateString(providerName, 32),
		ErrorType:    truncateString(errorType, 50),
		ErrorCode:    truncateString(errorCode, 50),
		StatusCode:   int32(statusCode),
		ErrorMessage: truncateString(errorMessage, 4000),
		ResponseBody: truncateString(responseBody, maxFailureResponseBodyLength),
		LatencyMs:    latencyMs,
		IsRetryable:  isRetryable,
	}

	_ = queries.CreateModelFailureLog(context.Background(), params)
}

func (t *FallbackTransport) logChannelFailure(req *http.Request, ch *db.Channel, resp *http.Response, roundTripErr error, responseBody string) {
	if t.DBQueries == nil || ch == nil {
		return
	}

	ctx := context.Background()
	requestID, _ := req.Context().Value(reqctx.KeyRequestID).(string)
	modelName, _ := req.Context().Value(reqctx.KeyPublicModelName).(string)
	if modelName == "" {
		modelName, _ = req.Context().Value(reqctx.KeyModelName).(string)
	}

	latencyMs := int32(0)
	if startedAt, ok := req.Context().Value(reqctx.KeyRequestStartTime).(time.Time); ok {
		latencyMs = int32(time.Since(startedAt).Milliseconds())
	}

	statusCode := int32(0)
	errorType := "transport_error"
	errorMessage := ""
	if roundTripErr != nil {
		errorMessage = roundTripErr.Error()
	} else if resp != nil {
		statusCode = int32(resp.StatusCode)
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			errorType = "rate_limit"
		case resp.StatusCode >= 500:
			errorType = "upstream_server_error"
		default:
			errorType = "upstream_error"
		}
		errorMessage = http.StatusText(resp.StatusCode)
	}

	snapshot := channel.GlobalManager.GetChannelHealthSnapshot(ch.ID)
	_ = t.DBQueries.CreateChannelFailureLog(ctx, db.CreateChannelFailureLogParams{
		ChannelID:       ch.ID,
		RequestID:       truncateString(requestID, 100),
		ModelName:       truncateString(modelName, 100),
		Provider:        truncateString(ch.Provider, 32),
		UpstreamBaseUrl: truncateString(ch.BaseUrl, 255),
		ErrorType:       truncateString(errorType, 50),
		StatusCode:      statusCode,
		ResponseBody:    truncateString(responseBody, maxFailureResponseBodyLength),
		ErrorMessage:    truncateString(errorMessage, 4000),
		LatencyMs:       latencyMs,
		CircuitState:    snapshot.CircuitState,
	})
}
