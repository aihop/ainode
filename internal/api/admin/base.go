package admin

import (
	"context"
	"encoding/json"
	"net/http"

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

func jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func errorResponse(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}
