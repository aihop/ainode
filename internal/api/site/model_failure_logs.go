package site

import (
	"net/http"
	"strconv"
	"time"

	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/utils"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/sync/errgroup"
)

func (h *InternalHandler) ModelFailureLogsListHandler(w http.ResponseWriter, r *http.Request) {
	userIDStr := r.Header.Get("X-Internal-User-Id")
	if userIDStr == "" {
		respondError(w, http.StatusUnauthorized, "Unauthorized: Missing X-Internal-User-Id header")
		return
	}

	userID, err := strconv.ParseInt(userIDStr, 10, 32)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid user_id format in header")
		return
	}

	modelName := r.URL.Query().Get("model")

	rangeStr := r.URL.Query().Get("range")
	days := 30
	switch rangeStr {
	case "24h":
		days = 1
	case "7d":
		days = 7
	case "30d":
		days = 30
	}
	startTime := time.Now().AddDate(0, 0, -days)
	pgStartTime := pgtype.Timestamptz{
		Time:  startTime,
		Valid: true,
	}

	pageStr := r.URL.Query().Get("page")
	limitStr := r.URL.Query().Get("pageSize")

	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	limit := 20
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
		limit = l
	}

	offset := (page - 1) * limit

	ctx := r.Context()
	eg, egCtx := errgroup.WithContext(ctx)

	var logs []db.ModelFailureLog
	var totalCount int64

	eg.Go(func() error {
		var queryErr error
		logs, queryErr = h.queries.GetUserModelFailureLogs(egCtx, db.GetUserModelFailureLogsParams{
			UserID:    int32(userID),
			ModelName: modelName,
			StartTime: pgStartTime,
			LimitVal:  int32(limit),
			OffsetVal: int32(offset),
		})
		return queryErr
	})

	eg.Go(func() error {
		var queryErr error
		totalCount, queryErr = h.queries.CountUserModelFailureLogs(egCtx, db.CountUserModelFailureLogsParams{
			UserID:    int32(userID),
			ModelName: modelName,
			StartTime: pgStartTime,
		})
		return queryErr
	})

	if err := eg.Wait(); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to fetch model failure logs: "+err.Error())
		return
	}

	type LogItem struct {
		ID          int64  `json:"id"`
		RequestID   string `json:"requestId"`
		Model       string `json:"model"`
		Provider    string `json:"provider"`
		ErrorType   string `json:"errorType"`
		ErrorCode   string `json:"errorCode"`
		StatusCode  int32  `json:"statusCode"`
		Message     string `json:"message"`
		Response    string `json:"response"`
		LatencyMs   int32  `json:"latencyMs"`
		IsRetryable bool   `json:"isRetryable"`
		APIKeyID    int32  `json:"apiKeyId,omitempty"`
		Time        string `json:"time"`
	}

	resLogs := make([]LogItem, 0, len(logs))
	for _, l := range logs {
		item := LogItem{
			ID:          l.ID,
			RequestID:   l.RequestID,
			Model:       l.ModelName,
			Provider:    l.Provider,
			ErrorType:   l.ErrorType,
			ErrorCode:   l.ErrorCode,
			StatusCode:  l.StatusCode,
			Message:     l.ErrorMessage,
			Response:    l.ResponseBody,
			LatencyMs:   l.LatencyMs,
			IsRetryable: l.IsRetryable,
			Time:        utils.FormatTime(l.CreatedAt),
		}
		if l.ApiKeyID.Valid {
			item.APIKeyID = l.ApiKeyID.Int32
		}
		resLogs = append(resLogs, item)
	}

	response := map[string]any{
		"list":     resLogs,
		"total":    totalCount,
		"page":     page,
		"pageSize": limit,
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"code": 0,
		"msg":  "success",
		"data": response,
	})
}
