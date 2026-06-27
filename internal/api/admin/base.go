package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aihop.io/ainode/internal/api/httpx"
	"aihop.io/ainode/internal/billing"
	"aihop.io/ainode/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AdminHandler struct {
	queries *db.Queries
	pool    *pgxpool.Pool
}

func NewAdminHandler(queries *db.Queries, pool *pgxpool.Pool) *AdminHandler {
	return &AdminHandler{
		queries: queries,
		pool:    pool,
	}
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

// jsonResponse 输出统一成功信封 {code:0,msg:"success",data}(见 docs/ai/api-conventions.md)。
func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "success", "data": data})
}

func errorResponse(w http.ResponseWriter, status int, message string) {
	httpx.Err(w, status, status, message)
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

func readAdminOperator(r *http.Request) (int32, string) {
	var adminID int32
	if raw := strings.TrimSpace(r.Header.Get("X-Internal-Admin-Id")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			adminID = int32(parsed)
		}
	}

	adminName := strings.TrimSpace(r.Header.Get("X-Internal-Admin-Username"))
	if adminName == "" && adminID > 0 {
		adminName = "admin#" + strconv.FormatInt(int64(adminID), 10)
	}

	return adminID, adminName
}
