package admin

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"

	"aihop.io/ainode/internal/billing"
	"aihop.io/ainode/internal/db"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type adminUserItem struct {
	ID               int32   `json:"id"`
	Email            string  `json:"email"`
	Nickname         string  `json:"nickname"`
	CashBalance      float64 `json:"cashBalance"`
	GrantBalance     float64 `json:"grantBalance"`
	AvailableBalance float64 `json:"availableBalance"`
	TotalRequests    int64   `json:"totalRequests"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	TotalSpend       float64 `json:"totalSpend"`
	ActiveKeyCount   int64   `json:"activeKeyCount"`
	Status           int32   `json:"status"`
	CreatedAt        string  `json:"createdAt"`
	LastLoginAt      string  `json:"lastLoginAt"`
	LastRequestAt    string  `json:"lastRequestAt"`
}

type adminUsersSummary struct {
	TotalUsers        int64   `json:"totalUsers"`
	ActiveUsers       int64   `json:"activeUsers"`
	TotalCashBalance  float64 `json:"totalCashBalance"`
	TotalGrantBalance float64 `json:"totalGrantBalance"`
	TotalBalance      float64 `json:"totalBalance"`
	TotalRequests     int64   `json:"totalRequests"`
	TotalTokens       int64   `json:"totalTokens"`
	TotalActiveKeys   int64   `json:"totalActiveKeys"`
}

type adminBalanceLogItem struct {
	ID              int64   `json:"id"`
	TransactionID   int64   `json:"transactionId"`
	UserID          int32   `json:"userId"`
	BalanceType     string  `json:"balanceType"`
	ActionType      string  `json:"actionType"`
	Amount          float64 `json:"amount"`
	BeforeBalance   float64 `json:"beforeBalance"`
	AfterBalance    float64 `json:"afterBalance"`
	OperatorAdminID int32   `json:"operatorAdminId"`
	OperatorName    string  `json:"operatorName"`
	Remark          string  `json:"remark"`
	CreatedAt       string  `json:"createdAt"`
}

func amountFromScaledInt(value pgtype.Int8) float64 {
	if value.Valid {
		return centsToMoney(value.Int64)
	}
	return 0
}

func moneyToScaledInt(amount float64) int64 {
	return int64(math.Round(amount * 100000000))
}

func parsePagination(r *http.Request, defaultPageSize int) (int, int, int) {
	page := 1
	pageSize := defaultPageSize

	if raw := r.URL.Query().Get("page"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			page = parsed
		}
	}

	if raw := r.URL.Query().Get("page_size"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 100 {
			pageSize = parsed
		}
	}

	offset := (page - 1) * pageSize
	return page, pageSize, offset
}

// ListUsers returns aggregated API customer stats for the ainode admin console.
func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	keyword := r.URL.Query().Get("keyword")
	page, pageSize, offset := parsePagination(r, 20)

	users, err := h.queries.ListUsersForAdmin(r.Context(), db.ListUsersForAdminParams{
		Keyword:   keyword,
		LimitVal:  int32(pageSize),
		OffsetVal: int32(offset),
	})
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to fetch users")
		return
	}

	total, err := h.queries.CountUsersForAdmin(r.Context(), keyword)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to count users")
		return
	}

	items := make([]adminUserItem, 0, len(users))
	for _, user := range users {
		cashBalance := amountFromScaledInt(user.CashBalance)
		grantBalance := amountFromScaledInt(user.GrantBalance)
		totalSpend := centsToMoney(user.TotalAmountCents)
		promptTokens := user.TotalPromptTokens
		completionTokens := user.TotalCompletionTokens
		totalRequests := user.TotalRequests
		activeKeyCount := user.ActiveKeyCount
		status := int32(0)
		if user.Status.Valid {
			status = user.Status.Int32
		}

		items = append(items, adminUserItem{
			ID:               user.ID,
			Email:            user.Email,
			Nickname:         user.Nickname,
			CashBalance:      cashBalance,
			GrantBalance:     grantBalance,
			AvailableBalance: cashBalance + grantBalance,
			TotalRequests:    totalRequests,
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
			TotalSpend:       totalSpend,
			ActiveKeyCount:   activeKeyCount,
			Status:           status,
			CreatedAt:        formatTime(user.CreatedAt),
			LastLoginAt:      formatTime(user.LastLoginAt),
			LastRequestAt:    formatTime(user.LastRequestAt),
		})
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"data":      items,
	})
}

// UsersSummary 返回管理员用户全局汇总（与列表拆开，避免列表翻页时重复跑全表聚合；
// 前端可单独、低频拉取并缓存）。
func (h *AdminHandler) UsersSummary(w http.ResponseWriter, r *http.Request) {
	keyword := r.URL.Query().Get("keyword")

	summary, err := h.queries.GetUsersSummaryForAdmin(r.Context(), keyword)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to summarize users")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"summary": adminUsersSummary{
			TotalUsers:        summary.TotalUsers,
			ActiveUsers:       summary.ActiveUsers,
			TotalCashBalance:  centsToMoney(summary.TotalCashBalance),
			TotalGrantBalance: centsToMoney(summary.TotalGrantBalance),
			TotalBalance:      centsToMoney(summary.TotalCashBalance + summary.TotalGrantBalance),
			TotalRequests:     summary.TotalRequests,
			TotalTokens:       summary.TotalTokens,
			TotalActiveKeys:   summary.TotalActiveKeys,
		},
	})
}

// ListUserBalanceLogs returns paginated admin balance change logs for a user.
func (h *AdminHandler) ListUserBalanceLogs(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		errorResponse(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	page, pageSize, offset := parsePagination(r, 10)

	items, err := h.queries.ListBalanceLogsByUser(r.Context(), db.ListBalanceLogsByUserParams{
		UserID:    int32(id),
		OffsetVal: int32(offset),
		LimitVal:  int32(pageSize),
	})
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to fetch balance logs")
		return
	}

	total, err := h.queries.CountBalanceLogsByUser(r.Context(), int32(id))
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to count balance logs")
		return
	}

	logs := make([]adminBalanceLogItem, 0, len(items))
	for _, item := range items {
		operatorAdminID := int32(0)
		if item.OperatorAdminID.Valid {
			operatorAdminID = item.OperatorAdminID.Int32
		}
		transactionID := int64(0)
		if item.TransactionID.Valid {
			transactionID = item.TransactionID.Int64
		}

		logs = append(logs, adminBalanceLogItem{
			ID:              item.ID,
			TransactionID:   transactionID,
			UserID:          item.UserID,
			BalanceType:     item.BalanceType,
			ActionType:      item.ActionType,
			Amount:          centsToMoney(item.AmountCents),
			BeforeBalance:   centsToMoney(item.BeforeBalanceCents),
			AfterBalance:    centsToMoney(item.AfterBalanceCents),
			OperatorAdminID: operatorAdminID,
			OperatorName:    item.OperatorName,
			Remark:          item.Remark,
			CreatedAt:       formatTime(item.CreatedAt),
		})
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"data":      logs,
	})
}

// AdjustUserBalance lets admins directly recharge cash balance or grant balance with an auditable log.
func (h *AdminHandler) AdjustUserBalance(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		errorResponse(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	var req struct {
		BalanceType string  `json:"balance_type"`
		Amount      float64 `json:"amount"`
		Remark      string  `json:"remark"`
	}
	if decodeErr := json.NewDecoder(r.Body).Decode(&req); decodeErr != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Amount <= 0 {
		errorResponse(w, http.StatusBadRequest, "Amount must be greater than 0")
		return
	}

	req.BalanceType = strings.TrimSpace(strings.ToLower(req.BalanceType))
	if req.BalanceType == "" {
		req.BalanceType = "cash"
	}
	if req.BalanceType != "cash" && req.BalanceType != "grant" {
		errorResponse(w, http.StatusBadRequest, "Unsupported balance type")
		return
	}

	req.Remark = strings.TrimSpace(req.Remark)
	if req.Remark == "" {
		errorResponse(w, http.StatusBadRequest, "Remark is required")
		return
	}

	scaledAmount := moneyToScaledInt(req.Amount)
	if scaledAmount <= 0 {
		errorResponse(w, http.StatusBadRequest, "Amount is too small")
		return
	}

	ctx := r.Context()
	operatorAdminID, operatorName := readAdminOperator(r)

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	txQueries := h.queries.WithTx(tx)
	user, err := txQueries.GetUserByIDForUpdate(ctx, int32(id))
	if err != nil {
		errorResponse(w, http.StatusNotFound, "User not found")
		return
	}

	beforeCash := int64(0)
	if user.CashBalance.Valid {
		beforeCash = user.CashBalance.Int64
	}
	beforeGrant := int64(0)
	if user.GrantBalance.Valid {
		beforeGrant = user.GrantBalance.Int64
	}

	beforeBalance := beforeCash
	afterBalance := beforeCash + scaledAmount

	if req.BalanceType == "grant" {
		beforeBalance = beforeGrant
		afterBalance = beforeGrant + scaledAmount
		err = txQueries.UpdateUserGrantBalance(ctx, db.UpdateUserGrantBalanceParams{
			ID:           int32(id),
			GrantBalance: pgtype.Int8{Int64: scaledAmount, Valid: true},
		})
	} else {
		err = txQueries.UpdateUserTopupBalance(ctx, db.UpdateUserTopupBalanceParams{
			ID:          int32(id),
			CashBalance: pgtype.Int8{Int64: scaledAmount, Valid: true},
		})
	}
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to update balance")
		return
	}

	operatorIDValue := pgtype.Int4{}
	if operatorAdminID > 0 {
		operatorIDValue = pgtype.Int4{Int32: operatorAdminID, Valid: true}
	}
	if operatorName == "" {
		operatorName = "system"
	}

	sourceID := ""
	if operatorAdminID > 0 {
		sourceID = strconv.FormatInt(int64(operatorAdminID), 10)
	}

	transaction, err := txQueries.CreateTransaction(ctx, db.CreateTransactionParams{
		UserID:             int32(id),
		EventID:            pgtype.Text{},
		Type:               "admin_adjust",
		BalanceType:        req.BalanceType,
		Direction:          "credit",
		AmountCents:        scaledAmount,
		BeforeBalanceCents: beforeBalance,
		AfterBalanceCents:  afterBalance,
		SourceType:         "admin",
		SourceID:           sourceID,
		Status:             "completed",
		Remark:             req.Remark,
		Metadata:           []byte("{}"),
	})
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to create transaction")
		return
	}

	err = txQueries.CreateBalanceLog(ctx, db.CreateBalanceLogParams{
		TransactionID:      pgtype.Int8{Int64: transaction.ID, Valid: true},
		UserID:             int32(id),
		BalanceType:        req.BalanceType,
		ActionType:         "admin_recharge",
		AmountCents:        scaledAmount,
		BeforeBalanceCents: beforeBalance,
		AfterBalanceCents:  afterBalance,
		OperatorAdminID:    operatorIDValue,
		OperatorName:       operatorName,
		Remark:             req.Remark,
	})
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to create balance log")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to commit balance update")
		return
	}

	// 用相对增减(INCRBY)同步缓存:只动被调整的那个池(cash/grant),不覆盖 sub_paid 等其它池,
	// 也避免用绝对值覆盖掉在途扣减。
	cacheSynced := true
	if err := billing.CreditBalanceCache(ctx, int32(id), req.BalanceType, scaledAmount); err != nil {
		cacheSynced = false
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"message":       "Balance updated successfully",
		"balanceType":   req.BalanceType,
		"amount":        req.Amount,
		"scaledAmount":  scaledAmount,
		"remark":        req.Remark,
		"cacheSynced":   cacheSynced,
		"transactionId": transaction.ID,
	})
}
