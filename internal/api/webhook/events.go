package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"aihop.io/ainode/internal/config"
)

type eventEnvelope struct {
	Event     string          `json:"event"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

type orderPaidEventData struct {
	ID           string                `json:"id"`
	Amount       float64               `json:"amount"`
	UserID       int32                 `json:"userId"`
	ContactEmail string                `json:"contactEmail"`
	MetaData     json.RawMessage       `json:"metaData"`
	Product      orderPaidEventProduct `json:"product"`
	Integration  orderIntegration      `json:"integration"`
}

type orderPaidEventProduct struct {
	ID       int32           `json:"id"`
	Slug     string          `json:"slug"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Price    float64         `json:"price"`
	MetaData json.RawMessage `json:"metaData"`
}

type orderIntegration struct {
	Transaction *orderIntegrationTransaction `json:"transaction"`
}

type orderIntegrationTransaction struct {
	Enabled     *bool           `json:"enabled"`
	Type        string          `json:"type"`
	BalanceType string          `json:"balance_type"`
	Direction   string          `json:"direction"`
	Amount      float64         `json:"amount"`
	SourceID    string          `json:"source_id"`
	Remark      string          `json:"remark"`
	Metadata    json.RawMessage `json:"metadata"`
}

func (h *Handler) HandleEvent(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(config.AppConfig.Internal.Token)
	if token == "" {
		errorResponse(w, http.StatusServiceUnavailable, "Internal token not configured")
		return
	}

	rawBody, err := ioReadAll(r.Body)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if !authorizeEventRequest(r, rawBody, token) {
		errorResponse(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var envelope eventEnvelope
	if err := json.Unmarshal(rawBody, &envelope); err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid event envelope")
		return
	}

	envelope.Event = strings.TrimSpace(envelope.Event)
	if envelope.Event == "" {
		errorResponse(w, http.StatusBadRequest, "Missing event name")
		return
	}

	switch envelope.Event {
	case "order.paid":
		h.handleOrderPaidEvent(w, r, envelope)
	default:
		jsonResponse(w, http.StatusOK, map[string]any{
			"message": "Event ignored",
			"ignored": true,
			"event":   envelope.Event,
		})
	}
}

func (h *Handler) handleOrderPaidEvent(w http.ResponseWriter, r *http.Request, envelope eventEnvelope) {
	var data orderPaidEventData
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid order.paid payload")
		return
	}

	req, ignoreReason, ok := buildTransactionRequestFromOrderPaid(envelope, data)
	if !ok {
		jsonResponse(w, http.StatusOK, map[string]any{
			"message": ignoreReason,
			"ignored": true,
			"event":   envelope.Event,
		})
		return
	}

	result, status, errMessage := h.processTransaction(r.Context(), req)
	if errMessage != "" {
		errorResponse(w, status, errMessage)
		return
	}

	jsonResponse(w, status, result)
}

func buildTransactionRequestFromOrderPaid(envelope eventEnvelope, data orderPaidEventData) (transactionWebhookRequest, string, bool) {
	if data.ID == "" {
		return transactionWebhookRequest{}, "Missing order ID", false
	}
	if data.UserID <= 0 {
		return transactionWebhookRequest{}, "Order has no linked user, event ignored", false
	}

	productMeta := rawJSONToMap(data.Product.MetaData)
	orderMeta := rawJSONToMap(data.MetaData)
	txConfig := data.Integration.Transaction

	if txConfig != nil && txConfig.Enabled != nil && !*txConfig.Enabled {
		return transactionWebhookRequest{}, "Transaction integration disabled", false
	}

	balanceType := strings.TrimSpace(strings.ToLower(firstNonEmpty(
		valueFromMap(orderMeta, "balance_type"),
		valueFromMap(productMeta, "balance_type"),
	)))
	txType := ""
	sourceID := data.ID
	remark := fmt.Sprintf("APayShop order paid: %s", data.ID)
	direction := "credit"
	amount := 0.0
	metadata := buildEventMetadata(envelope, data)
	explicitEnabled := false

	if txConfig != nil {
		explicitEnabled = txConfig.Enabled == nil || *txConfig.Enabled
		txType = strings.TrimSpace(strings.ToLower(txConfig.Type))
		balanceType = strings.TrimSpace(strings.ToLower(firstNonEmpty(txConfig.BalanceType, balanceType)))
		direction = strings.TrimSpace(strings.ToLower(firstNonEmpty(txConfig.Direction, direction)))
		sourceID = firstNonEmpty(txConfig.SourceID, sourceID)
		remark = firstNonEmpty(strings.TrimSpace(txConfig.Remark), remark)
		if txConfig.Amount > 0 {
			amount = txConfig.Amount
		}
		if len(txConfig.Metadata) > 0 && string(txConfig.Metadata) != "null" {
			metadata = txConfig.Metadata
		}
	}

	if amount <= 0 {
		amount = firstPositive(
			numberFromMap(orderMeta, "recharge_amount"),
			numberFromMap(productMeta, "recharge_amount"),
			data.Amount,
		)
	}

	if txType == "" {
		switch {
		case hasPlanIDs(orderMeta) || hasPlanIDs(productMeta) || data.Product.Type == "subscription":
			txType = "grant_issue"
		case balanceType != "":
			txType = "topup"
		}
	}

	if balanceType == "" {
		if txType == "grant_issue" || data.Product.Type == "subscription" || hasPlanIDs(orderMeta) || hasPlanIDs(productMeta) {
			balanceType = "grant"
		} else if txType == "topup" {
			balanceType = "cash"
		}
	}

	if !explicitEnabled && txType == "" && balanceType == "" {
		return transactionWebhookRequest{}, "No transaction mapping found in event payload", false
	}
	if amount <= 0 {
		return transactionWebhookRequest{}, "Transaction amount is missing or invalid", false
	}

	return transactionWebhookRequest{
		Source:      "apayshop",
		Event:       envelope.Event,
		EventID:     "apayshop:" + envelope.Event + ":" + data.ID,
		UserID:      data.UserID,
		Type:        txType,
		BalanceType: balanceType,
		Direction:   firstNonEmpty(direction, "credit"),
		Amount:      amount,
		SourceID:    sourceID,
		Remark:      remark,
		Metadata:    metadata,
	}, "", true
}

func authorizeEventRequest(r *http.Request, rawBody []byte, token string) bool {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "Bearer "+token {
		return true
	}

	timestamp := strings.TrimSpace(r.Header.Get("X-APayShop-Timestamp"))
	signature := strings.TrimSpace(r.Header.Get("X-APayShop-Signature"))
	if timestamp == "" || signature == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(rawBody)
	expected := mac.Sum(nil)

	actual, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}

	return hmac.Equal(actual, expected)
}

func ioReadAll(body io.Reader) ([]byte, error) {
	return io.ReadAll(body)
}

func buildEventMetadata(envelope eventEnvelope, data orderPaidEventData) []byte {
	payload, _ := json.Marshal(map[string]any{
		"event":     envelope.Event,
		"timestamp": envelope.Timestamp,
		"order": map[string]any{
			"id":           data.ID,
			"amount":       data.Amount,
			"userId":       data.UserID,
			"contactEmail": data.ContactEmail,
			"metaData":     rawJSONOrNil(data.MetaData),
		},
		"product": map[string]any{
			"id":       data.Product.ID,
			"slug":     data.Product.Slug,
			"name":     data.Product.Name,
			"type":     data.Product.Type,
			"price":    data.Product.Price,
			"metaData": rawJSONOrNil(data.Product.MetaData),
		},
	})
	return payload
}

func rawJSONToMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return map[string]any{}
	}
	return result
}

func rawJSONOrNil(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil
	}
	return result
}

func valueFromMap(m map[string]any, key string) string {
	value, ok := m[key]
	if !ok {
		return ""
	}
	if str, ok := value.(string); ok {
		return strings.TrimSpace(str)
	}
	return ""
}

func numberFromMap(m map[string]any, key string) float64 {
	value, ok := m[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	}
	return 0
}

func hasPlanIDs(m map[string]any) bool {
	value, ok := m["plan_ids"]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case []any:
		return len(typed) > 0
	case []string:
		return len(typed) > 0
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstPositive(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
