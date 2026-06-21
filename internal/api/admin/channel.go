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
		Name     string `json:"name"`
		Provider string `json:"provider"`
		BaseUrl  string `json:"base_url"`
		ApiKey   string `json:"api_key"`
		Weight   int32  `json:"weight"`
		Models   string `json:"models"`
		Status   int32  `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	channel, err := h.queries.CreateChannel(r.Context(), db.CreateChannelParams{
		Name:     req.Name,
		Provider: req.Provider,
		BaseUrl:  req.BaseUrl,
		ApiKey:   req.ApiKey,
		Weight:   pgtype.Int4{Int32: req.Weight, Valid: true},
		Models:   req.Models,
		Status:   pgtype.Int4{Int32: req.Status, Valid: true},
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
		Name     string `json:"name"`
		Provider string `json:"provider"`
		BaseUrl  string `json:"base_url"`
		ApiKey   string `json:"api_key"`
		Weight   int32  `json:"weight"`
		Models   string `json:"models"`
		Status   int32  `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	channel, err := h.queries.UpdateChannel(r.Context(), db.UpdateChannelParams{
		ID:       int32(id),
		Name:     req.Name,
		Provider: req.Provider,
		BaseUrl:  req.BaseUrl,
		ApiKey:   req.ApiKey,
		Weight:   pgtype.Int4{Int32: req.Weight, Valid: true},
		Models:   req.Models,
		Status:   pgtype.Int4{Int32: req.Status, Valid: true},
	})
	if err != nil {
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
