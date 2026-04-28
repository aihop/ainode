package site

import (
	"net/http"
	"strconv"
	"time"

	"fastix.ai/datapaas/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/sync/errgroup"
)

func (h *InternalHandler) BillingLogsListHandler(w http.ResponseWriter, r *http.Request) {
	// 1. 从 Header 中获取 user_id
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
	pgUserID := pgtype.Int4{
		Int32: int32(userID),
		Valid: true,
	}

	// 2. 解析查询参数
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
	limitStr := r.URL.Query().Get("limit")

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

	var logs []db.GetUserBillingLogsRow
	var totalCount int64

	// 查询数据
	eg.Go(func() error {
		var err error
		logs, err = h.queries.GetUserBillingLogs(egCtx, db.GetUserBillingLogsParams{
			UserID:    pgUserID,
			ModelName: modelName,
			StartTime: pgStartTime,
			LimitVal:  int32(limit),
			OffsetVal: int32(offset),
		})
		return err
	})

	// 查询总数
	eg.Go(func() error {
		var err error
		totalCount, err = h.queries.CountUserBillingLogs(egCtx, db.CountUserBillingLogsParams{
			UserID:    pgUserID,
			ModelName: modelName,
			StartTime: pgStartTime,
		})
		return err
	})

	if err := eg.Wait(); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to fetch billing logs: "+err.Error())
		return
	}

	// 3. 构建返回数据
	type LogItem struct {
		ID     string  `json:"id"`
		Time   string  `json:"time"`
		Model  string  `json:"model"`
		Input  int32   `json:"input"`
		Output int32   `json:"output"`
		Type   string  `json:"type"`
		Cost   float64 `json:"cost"`
	}
	resLogs := make([]LogItem, 0)
	for _, l := range logs {
		resLogs = append(resLogs, LogItem{
			ID:     formatUUID(l.ID),
			Time:   l.CreatedAt.Time.Format("2006-01-02 15:04:05"),
			Model:  l.ModelName,
			Input:  l.PromptTokens.Int32,
			Output: l.CompletionTokens.Int32,
			Type:   "flex",
			Cost:   centsToMoney(l.AmountCents),
		})
	}

	response := map[string]interface{}{
		"list":  resLogs,
		"total": totalCount,
		"page":  page,
		"limit": limit,
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 0,
		"msg":  "success",
		"data": response,
	})
}
