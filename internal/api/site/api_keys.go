package site

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"aihop.io/node-api/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

func generateAPIKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "sk-dp-" + hex.EncodeToString(b)
}

// MaskKey 隐藏中间部分，只显示首尾 (例如 sk-test-***-001)
func maskKey(key string) string {
	if len(key) <= 8 {
		return "******"
	}
	parts := strings.Split(key, "-")
	if len(parts) >= 3 {
		return parts[0] + "-" + parts[1] + "-***-" + parts[len(parts)-1]
	}
	return key[:4] + "***" + key[len(key)-4:]
}

func (h *InternalHandler) ListAPIKeysHandler(w http.ResponseWriter, r *http.Request) {
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

	// 2. 从数据库获取 Keys
	ctx := r.Context()
	keys, err := h.queries.GetUserAPIKeys(ctx, pgUserID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to fetch API keys: "+err.Error())
		return
	}

	// 3. 组装前端需要的格式
	type KeyItem struct {
		ID        int32  `json:"id"`
		Name      string `json:"name"`
		RawKey    string `json:"rawKey"`
		MaskedKey string `json:"maskedKey"`
		Model     string `json:"model"`
		Scope     string `json:"scope"`
		Status    string `json:"status"`
		QPS       string `json:"qps"`
		CreatedAt string `json:"createdAt"`
		ExpiresAt string `json:"expiresAt"`
		LastUsed  string `json:"lastUsed"`
	}

	var resKeys []KeyItem
	for _, k := range keys {
		// 解析允许的模型
		var models []string
		if len(k.AllowedModels) > 0 {
			_ = json.Unmarshal(k.AllowedModels, &models)
		}
		modelScope := "All Models"
		if len(models) > 0 {
			modelScope = strconv.Itoa(len(models)) + " Models"
		}

		// 状态
		status := "disabled"
		if k.Status.Int32 == 1 {
			status = "active"
		}

		resKeys = append(resKeys, KeyItem{
			ID:        k.ID,
			Name:      k.Name,
			RawKey:    k.KeyString,
			MaskedKey: maskKey(k.KeyString),
			Model:     "Relay V1",
			Scope:     modelScope,
			Status:    status,
			QPS:       "Unlimited",
			CreatedAt: k.CreatedAt.Time.Format("2006-01-02 15:04"),
			ExpiresAt: "Never", // 根据业务逻辑，如果有过期时间可以读取
			LastUsed:  "N/A",   // 需要单独记录
		})
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 0,
		"msg":  "success",
		"data": resKeys,
	})
}

// CreateKeyReq 创建 Key 的请求参数
type CreateKeyReq struct {
	Name   string   `json:"name"`
	Models []string `json:"models"`
}

func (h *InternalHandler) CreateAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	// 1. 获取 user_id
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

	// 2. 解析请求体
	var req CreateKeyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Name == "" {
		req.Name = "New API Key"
	}

	// 3. 处理模型列表
	modelsJSON, _ := json.Marshal(req.Models)
	if len(req.Models) == 0 {
		modelsJSON = []byte("[]") // 空数组代表允许所有模型
	}

	// 4. 生成 Key
	keyString := generateAPIKey()

	// 5. 写入数据库
	ctx := r.Context()
	newKey, err := h.queries.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		Name:          req.Name,
		KeyString:     keyString,
		UserID:        pgtype.Int4{Int32: int32(userID), Valid: true},
		AllowedModels: modelsJSON,
		Status:        pgtype.Int4{Int32: 1, Valid: true},
		TierLevel:     pgtype.Int4{Int32: 0, Valid: true},
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create API key: "+err.Error())
		return
	}

	// 6. 返回结果
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 0,
		"msg":  "success",
		"data": map[string]interface{}{
			"id":        newKey.ID,
			"name":      newKey.Name,
			"rawKey":    newKey.KeyString,
			"maskedKey": maskKey(newKey.KeyString),
		},
	})
}

// UpdateKeyStatusReq 启用/禁用 Key
type UpdateKeyStatusReq struct {
	ID     int32  `json:"id"`
	Status string `json:"status"` // "active" or "disabled"
}

func (h *InternalHandler) UpdateAPIKeyStatusHandler(w http.ResponseWriter, r *http.Request) {
	userIDStr := r.Header.Get("X-Internal-User-Id")
	userID, _ := strconv.ParseInt(userIDStr, 10, 32)
	var req UpdateKeyStatusReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	statusInt := int32(0)
	if req.Status == "active" {
		statusInt = 1
	}

	err := h.queries.UpdateAPIKeyStatus(r.Context(), db.UpdateAPIKeyStatusParams{
		Status: pgtype.Int4{Int32: statusInt, Valid: true},
		ID:     req.ID,
		UserID: pgtype.Int4{Int32: int32(userID), Valid: true},
	})

	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to update key status")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 0, "msg": "success"})
}

// DeleteKeyReq 删除 Key
type DeleteKeyReq struct {
	ID int32 `json:"id"`
}

func (h *InternalHandler) DeleteAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	userIDStr := r.Header.Get("X-Internal-User-Id")
	userID, _ := strconv.ParseInt(userIDStr, 10, 32)
	var req DeleteKeyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	err := h.queries.DeleteAPIKey(r.Context(), db.DeleteAPIKeyParams{
		ID:     req.ID,
		UserID: pgtype.Int4{Int32: int32(userID), Valid: true},
	})

	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to delete key")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 0, "msg": "success"})
}

// RotateKeyReq 轮换 Key
type RotateKeyReq struct {
	ID int32 `json:"id"`
}

func (h *InternalHandler) RotateAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	userIDStr := r.Header.Get("X-Internal-User-Id")
	userID, _ := strconv.ParseInt(userIDStr, 10, 32)
	var req RotateKeyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	newKeyString := generateAPIKey()
	err := h.queries.RotateAPIKey(r.Context(), db.RotateAPIKeyParams{
		KeyString: newKeyString,
		ID:        req.ID,
		UserID:    pgtype.Int4{Int32: int32(userID), Valid: true},
	})

	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to rotate key")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 0, "msg": "success"})
}

// UpdateKeyNameReq 编辑 Key 名称
type UpdateKeyNameReq struct {
	ID   int32  `json:"id"`
	Name string `json:"name"`
}

func (h *InternalHandler) UpdateAPIKeyNameHandler(w http.ResponseWriter, r *http.Request) {
	userIDStr := r.Header.Get("X-Internal-User-Id")
	userID, _ := strconv.ParseInt(userIDStr, 10, 32)
	var req UpdateKeyNameReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "Name cannot be empty")
		return
	}

	err := h.queries.UpdateAPIKeyName(r.Context(), db.UpdateAPIKeyNameParams{
		Name:   req.Name,
		ID:     req.ID,
		UserID: pgtype.Int4{Int32: int32(userID), Valid: true},
	})

	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to update key name")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"code": 0, "msg": "success"})
}
