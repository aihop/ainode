package site

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"time"

	"aihop.io/ainode/internal/db"

	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/sync/errgroup"
)

type InternalHandler struct {
	queries *db.Queries
}

func NewInternalHandler(queries *db.Queries) *InternalHandler {
	return &InternalHandler{queries: queries}
}

// 统一的错误返回帮助函数
func respondJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

// 金额转换助手 (将 10^8 的 BIGINT 转为正常的浮点数，如 100.00)
func centsToMoney(cents int64) float64 {
	return math.Round(float64(cents)/100000000.0*100) / 100
}

func formatUUID(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	src := u.Bytes
	return hex.EncodeToString(src[0:4]) + "-" +
		hex.EncodeToString(src[4:6]) + "-" +
		hex.EncodeToString(src[6:8]) + "-" +
		hex.EncodeToString(src[8:10]) + "-" +
		hex.EncodeToString(src[10:16])
}

func (h *InternalHandler) StatsHandler(w http.ResponseWriter, r *http.Request) {
	// 1. 从 Header 中获取通过 APayShop 代理层注入的 user_id
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

	// 2. 解析时间范围
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
	pgUserID := pgtype.Int4{
		Int32: int32(userID),
		Valid: true,
	}

	// 3. 并发查询聚合数据
	ctx := r.Context()
	eg, egCtx := errgroup.WithContext(ctx)

	var summary db.GetUserStatsSummaryRow
	var trendSeries []db.GetUserTrendSeriesRow
	var modelStats []db.GetUserModelStatsRow

	eg.Go(func() error {
		var err error
		summary, err = h.queries.GetUserStatsSummary(egCtx, db.GetUserStatsSummaryParams{
			UserID:    pgUserID,
			CreatedAt: pgStartTime,
		})
		// 忽略没找到记录的错误，没查到说明消耗为0
		if err != nil && err.Error() != "no rows in result set" {
			return err
		}
		return nil
	})

	eg.Go(func() error {
		var err error
		trendSeries, err = h.queries.GetUserTrendSeries(egCtx, db.GetUserTrendSeriesParams{
			UserID:    pgUserID,
			CreatedAt: pgStartTime,
		})
		return err
	})

	eg.Go(func() error {
		var err error
		modelStats, err = h.queries.GetUserModelStats(egCtx, db.GetUserModelStatsParams{
			UserID:    pgUserID,
			CreatedAt: pgStartTime,
		})
		return err
	})

	if err := eg.Wait(); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to fetch stats: "+err.Error())
		return
	}

	// 4. 组装 APayShop 前端所需的 JSON 结构

	// 计算总金额和订阅/弹性金额占比 (这里假设订阅赠送的金额占比，暂时简单处理)
	totalCost := centsToMoney(summary.TotalAmount)

	// 构建 TrendSeries
	type TrendItem struct {
		Key      string  `json:"key"`
		Label    string  `json:"label"`
		Requests int64   `json:"requests"`
		Cost     float64 `json:"cost"`
	}
	trends := make([]TrendItem, 0)
	for _, t := range trendSeries {
		label := ""
		if t.Date.Valid {
			label = t.Date.Time.Format("01-02")
		}
		trends = append(trends, TrendItem{
			Key:      label,
			Label:    label,
			Requests: t.RequestCount,
			Cost:     centsToMoney(t.DailyAmount),
		})
	}

	// 构建 ModelStats
	type ModelStatItem struct {
		Name  string  `json:"name"`
		Color string  `json:"color"`
		Value float64 `json:"value"`
	}
	models := make([]ModelStatItem, 0)
	palette := []string{"#2f6ea5", "#2f7f74", "#f59e0b", "#ef4444", "#8b5cf6", "#06b6d4"}
	for i, m := range modelStats {
		color := "#64748b"
		if i < len(palette) {
			color = palette[i]
		}
		models = append(models, ModelStatItem{
			Name:  m.ModelName,
			Color: color,
			Value: centsToMoney(m.TotalAmount),
		})
	}

	response := map[string]interface{}{
		"summary": map[string]interface{}{
			"totalCost":        totalCost,
			"subscriptionCost": 0, // 如果网关未来能区分哪部分钱扣自 grant_balance，可填充此值
			"flexCost":         totalCost,
			"subscriptionPct":  0,
			"flexPct":          100,
		},
		"usage": map[string]interface{}{
			"totalTokens":      summary.TotalPromptTokens + summary.TotalCompletionTokens,
			"inputTokens":      summary.TotalPromptTokens,
			"outputTokens":     summary.TotalCompletionTokens,
			"cacheReadTokens":  summary.TotalCacheHitTokens,
			"cacheWriteTokens": summary.TotalCacheMissTokens,
		},
		"trendSeries": trends,
		"modelStats":  models,
		// 状态码和延迟统计如果需要精确的话，需要记录在 billing_logs 中，这里暂时传 0 防止前端报错
		"statusCodes": map[string]int{"ok": 0, "client": 0, "server": 0},
		"errorRate":   0,
		"latency":     map[string]int{"p50": 0, "p95": 0},
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 0,
		"msg":  "success",
		"data": response,
	})
}
