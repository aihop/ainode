package admin

import (
	"net/http"

	"aihop.io/ainode/internal/api/httpx"
	"aihop.io/ainode/internal/db"
)

// ListBillingLogs
func (h *AdminHandler) ListBillingLogs(w http.ResponseWriter, r *http.Request) {
	page, pageSize, offset := httpx.ParsePage(r)

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

	httpx.Page(w, logs, page, pageSize, int64(total))
}
