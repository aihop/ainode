package site

import (
	"net/http"
	"strconv"
	"time"

	"aihop.io/ainode/internal/billing"
	"golang.org/x/sync/errgroup"
)

// WalletHandler 返回用户钱包概览：进账 / 用了 / 还剩 及余额明细。
// 职责单一，便于前端独立、按需高频刷新（余额变化快）。
func (h *InternalHandler) WalletHandler(w http.ResponseWriter, r *http.Request) {
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

	ctx := r.Context()
	eg, egCtx := errgroup.WithContext(ctx)

	var subBalance, grantBalance, cashBalance int64
	var totalSpend int64
	var totalFunded int64
	var subExpiresAt *string

	eg.Go(func() error {
		// 还剩：优先 Redis 实时余额，缺失回源 DB。
		if p, g, c, berr := billing.GetUserBalance(egCtx, h.queries, int32(userID)); berr == nil {
			subBalance, grantBalance, cashBalance = p, g, c
		}
		return nil
	})

	eg.Go(func() error {
		// 用了：累计消耗。
		if t, terr := h.queries.GetUserTotalSpend(egCtx, int32(userID)); terr == nil {
			totalSpend = t
		}
		return nil
	})

	eg.Go(func() error {
		// 进账：累计有效入账(充值+购买+套餐+直充，不含退款冲正)。
		if f, ferr := h.queries.GetUserTotalCredited(egCtx, int32(userID)); ferr == nil {
			totalFunded = f
		}
		return nil
	})

	eg.Go(func() error {
		// 订阅到期时间：sub/grant 两池清零时间点。
		user, uerr := h.queries.GetUserByID(egCtx, int32(userID))
		if uerr == nil && user.SubExpiresAt.Valid {
			iso := user.SubExpiresAt.Time.UTC().Format(time.RFC3339)
			subExpiresAt = &iso
		}
		return nil
	})

	_ = eg.Wait() // 各子查询失败均已降级为 0，不阻断整体返回

	available := subBalance + grantBalance + cashBalance

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 0,
		"msg":  "success",
		"data": map[string]interface{}{
			// 三件套（高精度金额 + 原始整数 *Cents）
			"funded":         centsToMoneyPrecise(totalFunded), // 进账
			"fundedCents":    totalFunded,
			"spent":          centsToMoneyPrecise(totalSpend), // 用了
			"spentCents":     totalSpend,
			"available":      centsToMoneyPrecise(available), // 还剩(三池之和)
			"availableCents": available,
			// 余额明细(消费顺序:订阅实付 → 订阅赠送 → 充值)
			"sub":        centsToMoneyPrecise(subBalance),
			"subCents":   subBalance,
			"grant":      centsToMoneyPrecise(grantBalance),
			"grantCents": grantBalance,
			"cash":       centsToMoneyPrecise(cashBalance),
			"cashCents":  cashBalance,
			// 订阅到期时间(ISO 8601)，无订阅则为 null
			"subExpiresAt": subExpiresAt,
		},
	})
}
