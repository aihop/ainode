package admin

import (
	"encoding/json"
	"net/http"

	"aihop.io/ainode/internal/db"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ListModels
func (h *AdminHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.queries.ListAllModelsForAdmin(r.Context())
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to fetch models")
		return
	}
	jsonResponse(w, http.StatusOK, models)
}

// CreateModel
func (h *AdminHandler) CreateModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelName           string  `json:"model_name"`
		InputPriceCents     int64   `json:"input_price_cents"`
		OutputPriceCents    int64   `json:"output_price_cents"`
		CacheHitPriceCents  int64   `json:"cache_hit_price_cents"`
		CacheMissPriceCents int64   `json:"cache_miss_price_cents"`
		Multiplier          float32 `json:"multiplier"`
		BillingPolicy       string  `json:"billing_policy"`
		MaxConcurrency      int32   `json:"max_concurrency"`
		Status              int32   `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.BillingPolicy == "" {
		req.BillingPolicy = "both"
	}

	model, err := h.queries.CreateModel(r.Context(), db.CreateModelParams{
		ModelName:           req.ModelName,
		InputPriceCents:     req.InputPriceCents,
		OutputPriceCents:    req.OutputPriceCents,
		CacheHitPriceCents:  req.CacheHitPriceCents,
		CacheMissPriceCents: req.CacheMissPriceCents,
		Multiplier:          req.Multiplier,
		BillingPolicy:       req.BillingPolicy,
		MaxConcurrency:      req.MaxConcurrency,
		Status:              pgtype.Int4{Int32: req.Status, Valid: true},
	})
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to create model")
		return
	}

	notifyConfigRefresh(r.Context())
	jsonResponse(w, http.StatusCreated, model)
}

// UpdateModel
func (h *AdminHandler) UpdateModel(w http.ResponseWriter, r *http.Request) {
	modelName := chi.URLParam(r, "model_name")
	if modelName == "" {
		errorResponse(w, http.StatusBadRequest, "Invalid model name")
		return
	}

	var req struct {
		InputPriceCents     int64   `json:"input_price_cents"`
		OutputPriceCents    int64   `json:"output_price_cents"`
		CacheHitPriceCents  int64   `json:"cache_hit_price_cents"`
		CacheMissPriceCents int64   `json:"cache_miss_price_cents"`
		Multiplier          float32 `json:"multiplier"`
		BillingPolicy       string  `json:"billing_policy"`
		MaxConcurrency      int32   `json:"max_concurrency"`
		Status              int32   `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.BillingPolicy == "" {
		req.BillingPolicy = "both"
	}

	model, err := h.queries.UpdateModel(r.Context(), db.UpdateModelParams{
		ModelName:           modelName,
		InputPriceCents:     req.InputPriceCents,
		OutputPriceCents:    req.OutputPriceCents,
		CacheHitPriceCents:  req.CacheHitPriceCents,
		CacheMissPriceCents: req.CacheMissPriceCents,
		Multiplier:          req.Multiplier,
		BillingPolicy:       req.BillingPolicy,
		MaxConcurrency:      req.MaxConcurrency,
		Status:              pgtype.Int4{Int32: req.Status, Valid: true},
	})
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to update model")
		return
	}

	notifyConfigRefresh(r.Context())
	jsonResponse(w, http.StatusOK, model)
}

// DeleteModel
func (h *AdminHandler) DeleteModel(w http.ResponseWriter, r *http.Request) {
	modelName := chi.URLParam(r, "model_name")
	if modelName == "" {
		errorResponse(w, http.StatusBadRequest, "Invalid model name")
		return
	}

	err := h.queries.DeleteModel(r.Context(), modelName)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to delete model")
		return
	}

	notifyConfigRefresh(r.Context())
	jsonResponse(w, http.StatusOK, map[string]string{"message": "Model deleted successfully"})
}
