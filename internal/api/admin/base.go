package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"aihop.io/ainode/internal/billing"
	"aihop.io/ainode/internal/db"
)

type AdminHandler struct {
	queries *db.Queries
}

func NewAdminHandler(queries *db.Queries) *AdminHandler {
	return &AdminHandler{queries: queries}
}

// 辅助方法：通知网关节点热更新配置
func notifyConfigRefresh(ctx context.Context) {
	billing.RedisClient.Publish(ctx, "ainode_config_refresh", "refresh")
}

func rawJSONOrDefault(raw json.RawMessage) []byte {
	if len(raw) == 0 || string(raw) == "null" {
		return []byte("{}")
	}
	return raw
}

func jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func errorResponse(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}

func centsToMoney(amount int64) float64 {
	return float64(amount) / 100000000
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
