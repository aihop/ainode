package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"aihop.io/ainode/internal/billing"
	"aihop.io/ainode/internal/channel"
	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/media"
	"aihop.io/ainode/internal/provider"
	"aihop.io/ainode/internal/utils"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type GatewayHandler struct {
	queries *db.Queries
}

func NewGatewayHandler(queries *db.Queries) *GatewayHandler {
	return &GatewayHandler{queries: queries}
}

func (h *GatewayHandler) CreateVideoGenerationTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	userID, ok := ctx.Value("user_id").(int32)
	if !ok {
		utils.WriteOpenAIError(w, http.StatusUnauthorized, "Invalid API Key", "invalid_request_error", "invalid_api_key")
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		utils.WriteOpenAIError(w, http.StatusBadRequest, "Cannot read request body", "invalid_request_error", "")
		return
	}
	defer r.Body.Close()

	req, err := media.ParseVideoGenerationRequest(bodyBytes)
	if err != nil || req.Model == "" {
		utils.WriteOpenAIError(w, http.StatusBadRequest, "Invalid video generation payload", "invalid_request_error", "")
		return
	}

	preDeducted, _ := ctx.Value("pre_deducted_cents").(int64)
	grantDeducted, _ := ctx.Value("grant_deducted").(int64)
	cashDeducted, _ := ctx.Value("cash_deducted").(int64)
	requestID, _ := ctx.Value("request_id").(string)
	if requestID == "" {
		requestID = uuid.NewString()
	}

	taskID := uuid.NewString()
	task, err := h.queries.CreateAsyncTask(ctx, db.CreateAsyncTaskParams{
		ID:               taskID,
		UserID:           userID,
		ChannelID:        pgtype.Int4{},
		RequestID:        requestID,
		TaskType:         "video_generation",
		Provider:         "",
		ModelName:        req.Model,
		Status:           "queued",
		UpstreamTaskID:   pgtype.Text{},
		InputPayload:     bodyBytes,
		OutputPayload:    emptyJSONObject(),
		ErrorPayload:     emptyJSONObject(),
		Metadata:         emptyJSONObject(),
		PreDeductedCents: preDeducted,
		GrantDeducted:    grantDeducted,
		CashDeducted:     cashDeducted,
		ActualCostCents:  0,
	})
	if err != nil {
		utils.WriteOpenAIError(w, http.StatusInternalServerError, "Failed to create task", "server_error", "")
		return
	}

	ch, err := channel.GlobalManager.GetNextChannelForCapabilities(req.Model, provider.ProviderCapabilities{
		Video:     true,
		AsyncTask: true,
	})
	if err != nil {
		task = h.failTaskAndRefund(ctx, task, http.StatusBadGateway, fmt.Sprintf("No async channel available for model: %s", req.Model))
		utils.WriteOpenAIError(w, http.StatusBadGateway, "No async channel available", "server_error", "async_channel_unavailable")
		return
	}

	driver := provider.GetProvider(ch.Provider)
	asyncAdapter := driver.Async()
	if asyncAdapter == nil {
		task = h.failTaskAndRefund(ctx, task, http.StatusBadGateway, fmt.Sprintf("provider %s does not support async tasks", ch.Provider))
		utils.WriteOpenAIError(w, http.StatusBadGateway, "Provider does not support async tasks", "server_error", "async_provider_unsupported")
		return
	}
	upstreamModelName := provider.ResolveUpstreamModelName(*ch, req.Model)
	submitResp, submitErr := asyncAdapter.SubmitTask(ctx, *ch, r.URL.Path, bodyBytes)
	if submitErr != nil || submitResp == nil || submitResp.StatusCode >= http.StatusBadRequest {
		statusCode := http.StatusBadGateway
		if submitResp != nil && submitResp.StatusCode > 0 {
			statusCode = submitResp.StatusCode
		}
		message := fmt.Sprintf("Upstream submit failed (status %d)", statusCode)
		if submitErr != nil {
			message = submitErr.Error()
		}
		task = h.failTaskAndRefund(ctx, task, http.StatusBadGateway, message)
		utils.WriteOpenAIError(w, http.StatusBadGateway, "Upstream video task submission failed", "server_error", "upstream_submit_failed")
		return
	}

	upstreamTaskID := submitResp.TaskID
	taskStatus := submitResp.Status
	metadataBytes := mustMarshalJSON(map[string]any{
		"public_model_name":   req.Model,
		"upstream_model_name": upstreamModelName,
		"upstream_status":     taskStatus,
		"refresh_path":        "/v1/tasks/" + upstreamTaskID,
		"cancel_path":         "/v1/tasks/" + upstreamTaskID + "/cancel",
	})

	task, err = h.queries.MarkAsyncTaskSubmitted(ctx, db.MarkAsyncTaskSubmittedParams{
		ID:             task.ID,
		ChannelID:      pgtype.Int4{Int32: ch.ID, Valid: true},
		Provider:       ch.Provider,
		Status:         taskStatus,
		UpstreamTaskID: pgtype.Text{String: upstreamTaskID, Valid: upstreamTaskID != ""},
		OutputPayload:  rawJSONOrDefaultBytes(submitResp.RawPayload),
		Metadata:       metadataBytes,
	})
	if err != nil {
		utils.WriteOpenAIError(w, http.StatusInternalServerError, "Failed to persist task submission", "server_error", "")
		return
	}

	if isTerminalTaskStatus(task.Status) {
		task = h.finalizeTerminalTask(ctx, task, submitResp.ParsedPayload)
	}

	writeJSON(w, http.StatusAccepted, taskResponse(task))
}

func (h *GatewayHandler) GetTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, ok := ctx.Value("user_id").(int32)
	if !ok {
		utils.WriteOpenAIError(w, http.StatusUnauthorized, "Invalid API Key", "invalid_request_error", "invalid_api_key")
		return
	}

	taskID := chi.URLParam(r, "task_id")
	task, err := h.queries.GetAsyncTaskByIDAndUser(ctx, db.GetAsyncTaskByIDAndUserParams{
		ID:     taskID,
		UserID: userID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			utils.WriteOpenAIError(w, http.StatusNotFound, "Task not found", "invalid_request_error", "task_not_found")
			return
		}
		utils.WriteOpenAIError(w, http.StatusInternalServerError, "Failed to fetch task", "server_error", "")
		return
	}

	if !isTerminalTaskStatus(task.Status) {
		if refreshed, refreshErr := h.refreshTaskStatus(ctx, task); refreshErr == nil {
			task = refreshed
		}
	}

	writeJSON(w, http.StatusOK, taskResponse(task))
}

func (h *GatewayHandler) CancelTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, ok := ctx.Value("user_id").(int32)
	if !ok {
		utils.WriteOpenAIError(w, http.StatusUnauthorized, "Invalid API Key", "invalid_request_error", "invalid_api_key")
		return
	}

	taskID := chi.URLParam(r, "task_id")
	task, err := h.queries.GetAsyncTaskByIDAndUser(ctx, db.GetAsyncTaskByIDAndUserParams{
		ID:     taskID,
		UserID: userID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			utils.WriteOpenAIError(w, http.StatusNotFound, "Task not found", "invalid_request_error", "task_not_found")
			return
		}
		utils.WriteOpenAIError(w, http.StatusInternalServerError, "Failed to fetch task", "server_error", "")
		return
	}

	if isTerminalTaskStatus(task.Status) {
		writeJSON(w, http.StatusOK, taskResponse(task))
		return
	}

	if task.ChannelID.Valid && task.UpstreamTaskID.Valid {
		ch, channelErr := h.queries.GetChannelByID(ctx, task.ChannelID.Int32)
		if channelErr == nil {
			driver := provider.GetProvider(ch.Provider)
			asyncAdapter := driver.Async()
			if asyncAdapter == nil {
				utils.WriteOpenAIError(w, http.StatusBadGateway, "Provider does not support async tasks", "server_error", "async_provider_unsupported")
				return
			}
			cancelResp, cancelErr := asyncAdapter.CancelTask(ctx, ch, task.UpstreamTaskID.String)
			if cancelErr != nil || cancelResp == nil || cancelResp.StatusCode >= http.StatusBadRequest {
				utils.WriteOpenAIError(w, http.StatusBadGateway, "Upstream task cancellation failed", "server_error", "upstream_cancel_failed")
				return
			}
		}
	}

	task, err = h.queries.MarkAsyncTaskStatus(ctx, db.MarkAsyncTaskStatusParams{
		ID:              task.ID,
		Status:          "canceled",
		OutputPayload:   task.OutputPayload,
		ErrorPayload:    mustMarshalJSON(map[string]any{"message": "Task canceled by user"}),
		Metadata:        task.Metadata,
		ActualCostCents: task.ActualCostCents,
	})
	if err != nil {
		utils.WriteOpenAIError(w, http.StatusInternalServerError, "Failed to cancel task", "server_error", "")
		return
	}

	if task.PreDeductedCents > 0 && task.ActualCostCents == 0 {
		billing.Refund(ctx, h.queries, task.UserID, task.PreDeductedCents, task.GrantDeducted, task.CashDeducted, task.RequestID)
	}

	writeJSON(w, http.StatusOK, taskResponse(task))
}

func (h *GatewayHandler) refreshTaskStatus(ctx context.Context, task db.AsyncTask) (db.AsyncTask, error) {
	if !task.ChannelID.Valid || !task.UpstreamTaskID.Valid {
		return task, nil
	}

	ch, err := h.queries.GetChannelByID(ctx, task.ChannelID.Int32)
	if err != nil || !ch.SupportsAsync {
		return task, err
	}

	driver := provider.GetProvider(ch.Provider)
	asyncAdapter := driver.Async()
	if asyncAdapter == nil {
		return task, fmt.Errorf("provider %s does not support async tasks", ch.Provider)
	}
	statusResp, reqErr := asyncAdapter.GetTask(ctx, ch, task.UpstreamTaskID.String)
	if reqErr != nil || statusResp == nil || statusResp.StatusCode >= http.StatusBadRequest {
		return task, reqErr
	}

	nextStatus := statusResp.Status
	task, err = h.queries.MarkAsyncTaskStatus(ctx, db.MarkAsyncTaskStatusParams{
		ID:              task.ID,
		Status:          nextStatus,
		OutputPayload:   rawJSONOrDefaultBytes(statusResp.RawPayload),
		ErrorPayload:    buildErrorPayload(nextStatus, statusResp.ParsedPayload),
		Metadata:        task.Metadata,
		ActualCostCents: task.ActualCostCents,
	})
	if err != nil {
		return task, err
	}

	if isTerminalTaskStatus(task.Status) {
		task = h.finalizeTerminalTask(ctx, task, statusResp.ParsedPayload)
	}

	return task, nil
}

func (h *GatewayHandler) finalizeTerminalTask(ctx context.Context, task db.AsyncTask, payload map[string]any) db.AsyncTask {
	switch task.Status {
	case "succeeded":
		if task.ActualCostCents > 0 {
			return task
		}
		actualCost := task.PreDeductedCents
		if err := billing.Settle(ctx, h.queries, billing.SettlementRequest{
			UserID:           task.UserID,
			ChannelID:        task.ChannelID.Int32,
			ModelName:        task.ModelName,
			PromptTokens:     0,
			CompletionTokens: 0,
			CacheHitTokens:   0,
			CacheMissTokens:  0,
			PreDeductedCents: task.PreDeductedCents,
			GrantDeducted:    task.GrantDeducted,
			CashDeducted:     task.CashDeducted,
			ActualCostCents:  actualCost,
			RequestID:        task.RequestID,
		}); err == nil {
			if updated, updateErr := h.queries.MarkAsyncTaskStatus(ctx, db.MarkAsyncTaskStatusParams{
				ID:              task.ID,
				Status:          task.Status,
				OutputPayload:   task.OutputPayload,
				ErrorPayload:    task.ErrorPayload,
				Metadata:        task.Metadata,
				ActualCostCents: actualCost,
			}); updateErr == nil {
				task = updated
			}
		}
	case "failed", "canceled":
		if task.PreDeductedCents > 0 && task.ActualCostCents == 0 {
			billing.Refund(ctx, h.queries, task.UserID, task.PreDeductedCents, task.GrantDeducted, task.CashDeducted, task.RequestID)
		}
	}

	if task.Status == "failed" && len(task.ErrorPayload) == 0 && payload != nil {
		if updated, err := h.queries.MarkAsyncTaskStatus(ctx, db.MarkAsyncTaskStatusParams{
			ID:              task.ID,
			Status:          task.Status,
			OutputPayload:   task.OutputPayload,
			ErrorPayload:    buildErrorPayload(task.Status, payload),
			Metadata:        task.Metadata,
			ActualCostCents: task.ActualCostCents,
		}); err == nil {
			task = updated
		}
	}

	return task
}

func (h *GatewayHandler) failTaskAndRefund(ctx context.Context, task db.AsyncTask, statusCode int, message string) db.AsyncTask {
	updated, err := h.queries.MarkAsyncTaskStatus(ctx, db.MarkAsyncTaskStatusParams{
		ID:              task.ID,
		Status:          "failed",
		OutputPayload:   task.OutputPayload,
		ErrorPayload:    mustMarshalJSON(map[string]any{"message": message, "status_code": statusCode}),
		Metadata:        task.Metadata,
		ActualCostCents: 0,
	})
	if err == nil {
		task = updated
	}

	if task.PreDeductedCents > 0 {
		billing.Refund(ctx, h.queries, task.UserID, task.PreDeductedCents, task.GrantDeducted, task.CashDeducted, task.RequestID)
	}
	return task
}

func taskResponse(task db.AsyncTask) map[string]any {
	resp := map[string]any{
		"id":         task.ID,
		"object":     "task",
		"type":       task.TaskType,
		"status":     task.Status,
		"model":      task.ModelName,
		"provider":   task.Provider,
		"request_id": task.RequestID,
		"input":      decodeJSONBytes(task.InputPayload),
		"output":     decodeJSONBytes(task.OutputPayload),
		"error":      nilIfEmptyJSON(task.ErrorPayload),
		"metadata":   decodeJSONBytes(task.Metadata),
		"usage": map[string]any{
			"pre_deducted_cents": task.PreDeductedCents,
			"actual_cost_cents":  task.ActualCostCents,
		},
		"created_at":   timestampUnix(task.CreatedAt),
		"updated_at":   timestampUnix(task.UpdatedAt),
		"submitted_at": timestampUnix(task.SubmittedAt),
		"finished_at":  timestampUnix(task.FinishedAt),
		"canceled_at":  timestampUnix(task.CanceledAt),
	}
	if task.UpstreamTaskID.Valid {
		resp["upstream_task_id"] = task.UpstreamTaskID.String
	}
	return resp
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func isTerminalTaskStatus(status string) bool {
	switch status {
	case "succeeded", "failed", "canceled":
		return true
	default:
		return false
	}
}

func buildErrorPayload(status string, payload map[string]any) []byte {
	if status != "failed" && status != "canceled" {
		return emptyJSONObject()
	}
	if payload == nil {
		return mustMarshalJSON(map[string]any{"message": "task failed"})
	}
	if errValue, ok := payload["error"]; ok {
		return mustMarshalJSON(errValue)
	}
	return mustMarshalJSON(map[string]any{
		"message": "task failed",
		"detail":  payload,
	})
}

func decodeJSONBytes(raw []byte) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return map[string]any{"raw": string(raw)}
	}
	return decoded
}

func nilIfEmptyJSON(raw []byte) any {
	decoded := decodeJSONBytes(raw)
	if asMap, ok := decoded.(map[string]any); ok && len(asMap) == 0 {
		return nil
	}
	return decoded
}

func rawJSONOrDefaultBytes(raw []byte) []byte {
	if len(raw) == 0 || string(raw) == "null" {
		return emptyJSONObject()
	}
	return raw
}

func emptyJSONObject() []byte {
	return []byte("{}")
}

func mustMarshalJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return emptyJSONObject()
	}
	return data
}

func timestampUnix(value pgtype.Timestamptz) any {
	if !value.Valid {
		return nil
	}
	return value.Time.Unix()
}
