package site

import (
	"net/http"
	"strconv"

	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/utils"
)

func (h *InternalHandler) BalanceLogsListHandler(w http.ResponseWriter, r *http.Request) {
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

	// 2. 解析分页参数
	pageStr := r.URL.Query().Get("page")
	limitStr := r.URL.Query().Get("pageSize")

	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}
	limit := 15
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
		limit = l
	}
	offset := (page - 1) * limit

	ctx := r.Context()

	// 3. 查询数据
	items, err := h.queries.ListBalanceLogsByUser(ctx, db.ListBalanceLogsByUserParams{
		UserID:    int32(userID),
		OffsetVal: int32(offset),
		LimitVal:  int32(limit),
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to fetch balance logs: "+err.Error())
		return
	}

	total, err := h.queries.CountBalanceLogsByUser(ctx, int32(userID))
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to count balance logs: "+err.Error())
		return
	}

	// 4. 构建返回数据
	type LogItem struct {
		ID            int64   `json:"id"`
		TransactionID int64   `json:"transactionId"`
		BalanceType   string  `json:"balanceType"`
		ActionType    string  `json:"actionType"`
		Amount        float64 `json:"amount"`
		BeforeBalance float64 `json:"beforeBalance"`
		AfterBalance  float64 `json:"afterBalance"`
		Remark        string  `json:"remark"`
		CreatedAt     string  `json:"createdAt"`
	}

	resLogs := make([]LogItem, 0, len(items))
	for _, item := range items {
		transactionID := int64(0)
		if item.TransactionID.Valid {
			transactionID = item.TransactionID.Int64
		}
		createdAt := ""
		if item.CreatedAt.Valid {
			createdAt = utils.FormatTime(item.CreatedAt.Time)
		}

		resLogs = append(resLogs, LogItem{
			ID:            item.ID,
			TransactionID: transactionID,
			BalanceType:   item.BalanceType,
			ActionType:    item.ActionType,
			Amount:        centsToMoney(item.AmountCents),
			BeforeBalance: centsToMoney(item.BeforeBalanceCents),
			AfterBalance:  centsToMoney(item.AfterBalanceCents),
			Remark:        item.Remark,
			CreatedAt:     createdAt,
		})
	}

	response := map[string]interface{}{
		"list":     resLogs,
		"total":    total,
		"page":     page,
		"pageSize": limit,
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 0,
		"msg":  "success",
		"data": response,
	})
}
