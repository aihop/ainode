package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"aihop.io/ainode/internal/billing"
	"aihop.io/ainode/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	queries *db.Queries
	pool    *pgxpool.Pool
}

func NewHandler(queries *db.Queries, pool *pgxpool.Pool) *Handler {
	return &Handler{
		queries: queries,
		pool:    pool,
	}
}

type transactionWebhookRequest struct {
	Source      string          `json:"source"`
	Event       string          `json:"event"`
	EventID     string          `json:"event_id"`
	UserID      int32           `json:"user_id"`
	Type        string          `json:"type"`
	BalanceType string          `json:"balance_type"`
	Direction   string          `json:"direction"`
	Amount      float64         `json:"amount"`
	SourceID    string          `json:"source_id"`
	Remark      string          `json:"remark"`
	Metadata    json.RawMessage `json:"metadata"`
}

type transactionProcessResult struct {
	Message          string `json:"message"`
	AlreadyProcessed bool   `json:"alreadyProcessed"`
	TransactionID    int64  `json:"transactionId"`
	Status           string `json:"status"`
	CacheSynced      bool   `json:"cacheSynced,omitempty"`
	CreatedAt        string `json:"createdAt"`
}

func (h *Handler) processTransaction(ctx context.Context, req transactionWebhookRequest) (transactionProcessResult, int, string) {
	req = normalizeTransactionRequest(req)

	if req.Event == "" || req.EventID == "" || req.UserID <= 0 || req.Type == "" || req.SourceID == "" {
		return transactionProcessResult{}, http.StatusBadRequest, "Missing required transaction fields"
	}
	if req.Amount <= 0 {
		return transactionProcessResult{}, http.StatusBadRequest, "Amount must be greater than 0"
	}
	if req.BalanceType != "cash" && req.BalanceType != "grant" {
		return transactionProcessResult{}, http.StatusBadRequest, "Unsupported balance type"
	}
	if req.Direction != "credit" && req.Direction != "debit" {
		return transactionProcessResult{}, http.StatusBadRequest, "Unsupported direction"
	}

	scaledAmount := moneyToScaledInt(req.Amount)
	if scaledAmount <= 0 {
		return transactionProcessResult{}, http.StatusBadRequest, "Amount is too small"
	}

	eventIDValue := pgtype.Text{String: req.EventID, Valid: req.EventID != ""}
	if existing, err := h.queries.GetTransactionByEventID(ctx, eventIDValue); err == nil {
		return transactionProcessResult{
			Message:          "Transaction already processed",
			AlreadyProcessed: true,
			TransactionID:    existing.ID,
			Status:           existing.Status,
			CreatedAt:        formatTime(existing.CreatedAt),
		}, http.StatusOK, ""
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return transactionProcessResult{}, http.StatusInternalServerError, "Failed to check transaction idempotency"
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return transactionProcessResult{}, http.StatusInternalServerError, "Failed to start transaction"
	}
	defer tx.Rollback(ctx)

	txQueries := h.queries.WithTx(tx)
	user, err := txQueries.GetUserByIDForUpdate(ctx, req.UserID)
	if err != nil {
		return transactionProcessResult{}, http.StatusNotFound, "User not found"
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
	if req.BalanceType == "grant" {
		beforeBalance = beforeGrant
	}

	delta := scaledAmount
	if req.Direction == "debit" {
		delta = -scaledAmount
	}
	afterBalance := beforeBalance + delta
	if afterBalance < 0 {
		return transactionProcessResult{}, http.StatusConflict, "Insufficient balance for debit transaction"
	}

	if req.BalanceType == "grant" {
		err = txQueries.UpdateUserGrantBalance(ctx, db.UpdateUserGrantBalanceParams{
			ID:           req.UserID,
			GrantBalance: pgtype.Int8{Int64: delta, Valid: true},
		})
	} else {
		err = txQueries.UpdateUserTopupBalance(ctx, db.UpdateUserTopupBalanceParams{
			ID:          req.UserID,
			CashBalance: pgtype.Int8{Int64: delta, Valid: true},
		})
	}
	if err != nil {
		return transactionProcessResult{}, http.StatusInternalServerError, "Failed to update user balance"
	}

	transaction, err := txQueries.CreateTransaction(ctx, db.CreateTransactionParams{
		UserID:             req.UserID,
		EventID:            eventIDValue,
		Type:               req.Type,
		BalanceType:        req.BalanceType,
		Direction:          req.Direction,
		AmountCents:        scaledAmount,
		BeforeBalanceCents: beforeBalance,
		AfterBalanceCents:  afterBalance,
		SourceType:         req.Source,
		SourceID:           req.SourceID,
		Status:             "completed",
		Remark:             req.Remark,
		Metadata:           normalizeMetadata(req.Source, req.Event, req.Metadata),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			existing, getErr := h.queries.GetTransactionByEventID(ctx, eventIDValue)
			if getErr == nil {
				return transactionProcessResult{
					Message:          "Transaction already processed",
					AlreadyProcessed: true,
					TransactionID:    existing.ID,
					Status:           existing.Status,
					CreatedAt:        formatTime(existing.CreatedAt),
				}, http.StatusOK, ""
			}
		}
		return transactionProcessResult{}, http.StatusInternalServerError, "Failed to create transaction"
	}

	if err := tx.Commit(ctx); err != nil {
		return transactionProcessResult{}, http.StatusInternalServerError, "Failed to commit transaction"
	}

	// 用相对增减(INCRBY)而非绝对 SET 同步缓存，避免覆盖请求侧在途的 DECRBY 扣减
	// （否则存在竞态：充值把缓存覆盖成 DB 值，丢掉尚未回写 DB 的实时扣减 → 用户可超额消费）。
	cacheSynced := true
	if err := billing.CreditBalanceCache(ctx, req.UserID, req.BalanceType, delta); err != nil {
		cacheSynced = false
	}

	return transactionProcessResult{
		Message:          "Transaction processed successfully",
		AlreadyProcessed: false,
		TransactionID:    transaction.ID,
		Status:           transaction.Status,
		CacheSynced:      cacheSynced,
		CreatedAt:        formatTime(transaction.CreatedAt),
	}, http.StatusOK, ""
}

func normalizeTransactionRequest(req transactionWebhookRequest) transactionWebhookRequest {
	req.Source = strings.TrimSpace(strings.ToLower(req.Source))
	req.Event = strings.TrimSpace(req.Event)
	req.EventID = strings.TrimSpace(req.EventID)
	req.Type = strings.TrimSpace(strings.ToLower(req.Type))
	req.BalanceType = strings.TrimSpace(strings.ToLower(req.BalanceType))
	req.Direction = strings.TrimSpace(strings.ToLower(req.Direction))
	req.SourceID = strings.TrimSpace(req.SourceID)
	req.Remark = strings.TrimSpace(req.Remark)

	if req.Source == "" {
		req.Source = "apayshop"
	}
	if req.Remark == "" {
		req.Remark = req.Source + ":" + req.Event
	}

	return req
}

func normalizeMetadata(source, event string, raw json.RawMessage) []byte {
	if len(raw) == 0 || string(raw) == "null" {
		payload, _ := json.Marshal(map[string]any{
			"source": source,
			"event":  event,
		})
		return payload
	}
	return raw
}

func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func errorResponse(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}

func moneyToScaledInt(amount float64) int64 {
	return int64(math.Round(amount * 100000000))
}

func formatTime(value any) string {
	switch v := value.(type) {
	case interface{ Value() (time.Time, bool) }:
		if ts, ok := v.Value(); ok {
			return ts.UTC().Format(time.RFC3339)
		}
	case time.Time:
		return v.UTC().Format(time.RFC3339)
	}
	return ""
}
