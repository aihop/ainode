package admin

import (
	"net/http"
	"strconv"

	"aihop.io/ainode/internal/api/httpx"
	"aihop.io/ainode/internal/channel"
	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/utils"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type adminChannelHealthItem struct {
	ID                  int32  `json:"id"`
	Name                string `json:"name"`
	Provider            string `json:"provider"`
	BaseURL             string `json:"baseURL"`
	Models              string `json:"models"`
	ProtocolType        string `json:"protocolType"`
	UploadMode          string `json:"uploadMode"`
	SupportsAsync       bool   `json:"supportsAsync"`
	Weight              int32  `json:"weight"`
	Status              int32  `json:"status"`
	CircuitState        string `json:"circuitState"`
	ConsecutiveFailures int64  `json:"consecutiveFailures"`
	ProbeInFlight       bool   `json:"probeInFlight"`
	CooldownSeconds     int64  `json:"cooldownSeconds"`
	LastFailureAt       string `json:"lastFailureAt"`
	LastSuccessAt       string `json:"lastSuccessAt"`
}

type adminChannelFailureLogItem struct {
	ID              int64  `json:"id"`
	ChannelID       int32  `json:"channelId"`
	RequestID       string `json:"requestId"`
	ModelName       string `json:"modelName"`
	Provider        string `json:"provider"`
	UpstreamBaseURL string `json:"upstreamBaseURL"`
	ErrorType       string `json:"errorType"`
	StatusCode      int32  `json:"statusCode"`
	ResponseBody    string `json:"responseBody"`
	ErrorMessage    string `json:"errorMessage"`
	LatencyMs       int32  `json:"latencyMs"`
	CircuitState    string `json:"circuitState"`
	CreatedAt       string `json:"createdAt"`
}

func readOptionalInt32(value pgtype.Int4) int32 {
	if value.Valid {
		return value.Int32
	}
	return 0
}

func (h *AdminHandler) ListChannelHealth(w http.ResponseWriter, r *http.Request) {
	channels, err := h.queries.ListAllChannels(r.Context())
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to fetch channel health")
		return
	}

	items := make([]adminChannelHealthItem, 0, len(channels))
	for _, item := range channels {
		snapshot := channel.HealthSnapshot{
			ChannelID:       item.ID,
			CircuitState:    "closed",
			CooldownSeconds: 30,
		}
		if channel.GlobalManager != nil {
			snapshot = channel.GlobalManager.GetChannelHealthSnapshot(item.ID)
		}

		items = append(items, adminChannelHealthItem{
			ID:                  item.ID,
			Name:                item.Name,
			Provider:            item.Provider,
			BaseURL:             item.BaseUrl,
			Models:              item.Models,
			ProtocolType:        item.ProtocolType,
			UploadMode:          item.UploadMode,
			SupportsAsync:       item.SupportsAsync,
			Weight:              readOptionalInt32(item.Weight),
			Status:              readOptionalInt32(item.Status),
			CircuitState:        snapshot.CircuitState,
			ConsecutiveFailures: snapshot.FailureCount,
			ProbeInFlight:       snapshot.ProbeInFlight,
			CooldownSeconds:     snapshot.CooldownSeconds,
			LastFailureAt:       utils.FormatTime(snapshot.LastFailureAt),
			LastSuccessAt:       utils.FormatTime(snapshot.LastSuccessAt),
		})
	}

	jsonResponse(w, http.StatusOK, items)
}

// ResetChannelCircuitBreaker 手动重置渠道断路器。
func (h *AdminHandler) ResetChannelCircuitBreaker(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		errorResponse(w, http.StatusBadRequest, "Invalid channel ID")
		return
	}

	if channel.GlobalManager != nil {
		channel.GlobalManager.ResetCircuitBreaker(int32(id))
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"message":    "Circuit breaker reset",
		"channel_id": id,
	})
}

// ProbeChannel 手动探测渠道。
func (h *AdminHandler) ProbeChannel(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		errorResponse(w, http.StatusBadRequest, "Invalid channel ID")
		return
	}

	if channel.GlobalManager != nil {
		channel.GlobalManager.ProbeChannel(int32(id))
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"message":    "Probe scheduled",
		"channel_id": id,
	})
}

func (h *AdminHandler) ListChannelFailureLogs(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		errorResponse(w, http.StatusBadRequest, "Invalid channel ID")
		return
	}

	page, pageSize, offset := parsePagination(r, 10)

	items, err := h.queries.ListChannelFailureLogsByChannel(r.Context(), db.ListChannelFailureLogsByChannelParams{
		ChannelID: int32(id),
		OffsetVal: int32(offset),
		LimitVal:  int32(pageSize),
	})
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to fetch channel failure logs")
		return
	}

	total, err := h.queries.CountChannelFailureLogsByChannel(r.Context(), int32(id))
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to count channel failure logs")
		return
	}

	logs := make([]adminChannelFailureLogItem, 0, len(items))
	for _, item := range items {
		logs = append(logs, adminChannelFailureLogItem{
			ID:              item.ID,
			ChannelID:       item.ChannelID,
			RequestID:       item.RequestID,
			ModelName:       item.ModelName,
			Provider:        item.Provider,
			UpstreamBaseURL: item.UpstreamBaseUrl,
			ErrorType:       item.ErrorType,
			StatusCode:      item.StatusCode,
			ResponseBody:    item.ResponseBody,
			ErrorMessage:    item.ErrorMessage,
			LatencyMs:       item.LatencyMs,
			CircuitState:    item.CircuitState,
			CreatedAt:       utils.FormatTime(item.CreatedAt),
		})
	}

	httpx.Page(w, logs, page, pageSize, int64(total))
}
