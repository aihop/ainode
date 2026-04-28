package admin

import (
	"net/http"
	"strconv"

	"fastix.ai/datapaas/internal/db"
)

// ListBillingLogs
func (h *AdminHandler) ListBillingLogs(w http.ResponseWriter, r *http.Request) {
	pageStr := r.URL.Query().Get("page")
	pageSizeStr := r.URL.Query().Get("page_size")

	page := 1
	pageSize := 20

	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}
	if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 {
		pageSize = ps
	}

	offset := (page - 1) * pageSize

	logs, err := h.queries.ListBillingLogs(r.Context(), db.ListBillingLogsParams{
		Limit:  int32(pageSize),
		Offset: int32(offset),
	})
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to fetch billing logs")
		return
	}

	total, err := h.queries.CountBillingLogs(r.Context())
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to count billing logs")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"data":      logs,
	})
}