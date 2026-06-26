package admin

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"

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

func amountFromScaledInt(value pgtype.Int8) float64 {
	if value.Valid {
		return centsToMoney(value.Int64)
	}
	return 0
}

func moneyToScaledInt(amount float64) int64 {
	return int64(math.Round(amount * 100000000))
}

// ListUsers returns aggregated API customer stats for the ainode admin console.
func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	pageStr := r.URL.Query().Get("page")
	pageSizeStr := r.URL.Query().Get("page_size")
	keyword := r.URL.Query().Get("keyword")

	page := 1
	pageSize := 20

	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}
	if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 && ps <= 100 {
		pageSize = ps
	}

	offset := (page - 1) * pageSize

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

	summary, err := h.queries.GetUsersSummaryForAdmin(r.Context(), keyword)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to summarize users")
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

	jsonResponse(w, http.StatusOK, map[string]interface{}{
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
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"data":      items,
	})
}

// AdjustUserBalance lets admins directly recharge cash balance or grant balance.
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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Amount <= 0 {
		errorResponse(w, http.StatusBadRequest, "Amount must be greater than 0")
		return
	}

	scaledAmount := moneyToScaledInt(req.Amount)
	if scaledAmount <= 0 {
		errorResponse(w, http.StatusBadRequest, "Amount is too small")
		return
	}

	switch req.BalanceType {
	case "grant":
		err = h.queries.UpdateUserGrantBalance(r.Context(), db.UpdateUserGrantBalanceParams{
			ID:           int32(id),
			GrantBalance: pgtype.Int8{Int64: scaledAmount, Valid: true},
		})
	default:
		err = h.queries.UpdateUserTopupBalance(r.Context(), db.UpdateUserTopupBalanceParams{
			ID:          int32(id),
			CashBalance: pgtype.Int8{Int64: scaledAmount, Valid: true},
		})
	}
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to update balance")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"message":      "Balance updated successfully",
		"balanceType":  req.BalanceType,
		"amount":       req.Amount,
		"scaledAmount": scaledAmount,
	})
}
