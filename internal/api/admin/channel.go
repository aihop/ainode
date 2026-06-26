package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"aihop.io/ainode/internal/db"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ListChannels
func (h *AdminHandler) ListChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := h.queries.ListAllChannels(r.Context())
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to fetch channels")
		return
	}
	jsonResponse(w, http.StatusOK, channels)
}

// CreateChannel
func (h *AdminHandler) CreateChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string          `json:"name"`
		Provider      string          `json:"provider"`
		BaseUrl       string          `json:"baseUrl"`
		ApiKey        string          `json:"apiKey"`
		Weight        int32           `json:"weight"`
		Models        string          `json:"models"`
		ProtocolType  string          `json:"protocolType"`
		UploadMode    string          `json:"uploadMode"`
		ModelMapping  json.RawMessage `json:"modelMapping"`
		SupportsAsync bool            `json:"supportsAsync"`
		Status        int32           `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.ProtocolType == "" {
		req.ProtocolType = "openai"
	}
	if req.UploadMode == "" {
		req.UploadMode = "url"
	}

	channel, err := h.queries.CreateChannel(r.Context(), db.CreateChannelParams{
		Name:          req.Name,
		Provider:      req.Provider,
		BaseUrl:       req.BaseUrl,
		ApiKey:        req.ApiKey,
		Weight:        pgtype.Int4{Int32: req.Weight, Valid: true},
		Models:        req.Models,
		ProtocolType:  req.ProtocolType,
		UploadMode:    req.UploadMode,
		ModelMapping:  rawJSONOrDefault(req.ModelMapping),
		SupportsAsync: req.SupportsAsync,
		Status:        pgtype.Int4{Int32: req.Status, Valid: true},
	})
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to create channel")
		return
	}

	notifyConfigRefresh(r.Context())
	jsonResponse(w, http.StatusCreated, channel)
}

// UpdateChannel
func (h *AdminHandler) UpdateChannel(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid channel ID")
		return
	}

	var req struct {
		Name          string          `json:"name"`
		Provider      string          `json:"provider"`
		BaseUrl       string          `json:"baseUrl"`
		ApiKey        string          `json:"apiKey"`
		Weight        int32           `json:"weight"`
		Models        string          `json:"models"`
		ProtocolType  string          `json:"protocolType"`
		UploadMode    string          `json:"uploadMode"`
		ModelMapping  json.RawMessage `json:"modelMapping"`
		SupportsAsync bool            `json:"supportsAsync"`
		Status        int32           `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.ProtocolType == "" {
		req.ProtocolType = "openai"
	}
	if req.UploadMode == "" {
		req.UploadMode = "url"
	}

	channel, updateErr := h.queries.UpdateChannel(r.Context(), db.UpdateChannelParams{
		ID:            int32(id),
		Name:          req.Name,
		Provider:      req.Provider,
		BaseUrl:       req.BaseUrl,
		ApiKey:        req.ApiKey,
		Weight:        pgtype.Int4{Int32: req.Weight, Valid: true},
		Models:        req.Models,
		ProtocolType:  req.ProtocolType,
		UploadMode:    req.UploadMode,
		ModelMapping:  rawJSONOrDefault(req.ModelMapping),
		SupportsAsync: req.SupportsAsync,
		Status:        pgtype.Int4{Int32: req.Status, Valid: true},
	})
	if updateErr != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to update channel")
		return
	}

	notifyConfigRefresh(r.Context())
	jsonResponse(w, http.StatusOK, channel)
}

// DeleteChannel
func (h *AdminHandler) DeleteChannel(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid channel ID")
		return
	}

	err = h.queries.DeleteChannel(r.Context(), int32(id))
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to delete channel")
		return
	}

	notifyConfigRefresh(r.Context())
	jsonResponse(w, http.StatusOK, map[string]string{"message": "Channel deleted successfully"})
}
