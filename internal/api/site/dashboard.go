package site

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"aihop.io/ainode/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/sync/errgroup"
)

func (h *InternalHandler) DashboardHandler(w http.ResponseWriter, r *http.Request) {
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

	pgUserID := pgtype.Int4{
		Int32: int32(userID),
		Valid: true,
	}

	// 2. 固定获取最近 30 天的日志数据用于热力图
	startTime := time.Now().AddDate(0, 0, -30)
	pgStartTime := pgtype.Timestamptz{
		Time:  startTime,
		Valid: true,
	}

	// 3. 并发查询
	ctx := r.Context()
	eg, egCtx := errgroup.WithContext(ctx)

	var trendSeries []db.GetUserTrendSeriesRow
	var activeKeysCount int64
	var summary db.GetUserStatsSummaryRow
	var userKeys []db.GetUserAPIKeysRow

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
		activeKeysCount, err = h.queries.CountActiveUserAPIKeys(egCtx, pgUserID)
		return err
	})

	eg.Go(func() error {
		var err error
		userKeys, err = h.queries.GetUserAPIKeys(egCtx, pgUserID)
		return err
	})

	eg.Go(func() error {
		var err error
		summary, err = h.queries.GetUserStatsSummary(egCtx, db.GetUserStatsSummaryParams{
			UserID:    pgUserID,
			CreatedAt: pgStartTime,
		})
		if err != nil && err.Error() != "no rows in result set" {
			return err
		}
		return nil
	})

	if err := eg.Wait(); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to fetch dashboard stats: "+err.Error())
		return
	}

	// 4. 组装数据返回给 APayShop (Node.js) 聚合层

	// 构建热力图数据 (只返回日期和数量)
	type HeatmapItem struct {
		Date  string `json:"date"`
		Count int64  `json:"count"`
	}
	heatmap := make([]HeatmapItem, 0, len(trendSeries))
	for _, t := range trendSeries {
		if t.Date.Valid {
			heatmap = append(heatmap, HeatmapItem{
				Date:  t.Date.Time.Format("2006-01-02"),
				Count: t.RequestCount,
			})
		}
	}

	// 计算 API 额度 (将 10^8 的 BIGINT 转换为美元或其他浮点数显示，或者直接返回供前端处理)
	var totalQuotaLimit int64 = 0
	var totalQuotaUsed int64 = 0
	for _, k := range userKeys {
		if k.QuotaLimit.Valid {
			totalQuotaLimit += k.QuotaLimit.Int64
		}
		if k.QuotaUsed.Valid {
			totalQuotaUsed += k.QuotaUsed.Int64
		}
	}

	percentage := 0.0
	if totalQuotaLimit > 0 {
		percentage = float64(totalQuotaUsed) / float64(totalQuotaLimit) * 100
		if percentage > 100 {
			percentage = 100
		}
	}

	// 构建返回的 JSON
	response := map[string]interface{}{
		"activeKeysCount": activeKeysCount,
		"totalCallsThisMonth": func() int64 {
			var total int64 = 0
			for _, t := range trendSeries {
				total += t.RequestCount
			}
			return total
		}(),
		"activityHeatmap": heatmap,
		"usage": map[string]interface{}{
			"inputTokens":  summary.TotalPromptTokens,
			"outputTokens": summary.TotalCompletionTokens,
		},
		"quota": map[string]interface{}{
			"limit":      totalQuotaLimit,
			"used":       totalQuotaUsed,
			"percentage": math.Round(percentage),
		},
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 0,
		"msg":  "success",
		"data": response,
	})
}
