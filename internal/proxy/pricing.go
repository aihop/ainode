package proxy

import "aihop.io/ainode/internal/utils"

// settlementPricing 是结算计价所需的模型价格（均为 10^8 放大的整数 / 倍率）。
type settlementPricing struct {
	InputPrice     int64
	OutputPrice    int64
	CacheHitPrice  int64
	CacheMissPrice int64
	Multiplier     float32
	PricingMode    string // token | request
	RequestPrice   int64  // 按次计费单价（pricingMode=request 时使用）
}

// usageTokens 是单次请求的 token 用量。
type usageTokens struct {
	Prompt     int
	Completion int
	CacheHit   int
	CacheMiss  int
}

// computeActualCost 计算单次请求的实际费用（已应用倍率，四舍五入），单位与 amount_cents 一致。
//
// 计价规则：
//   - pricingMode=request 或图像生成：按次价 × 计费单位；
//   - 否则按 token：无缓存明细时 prompt 全按 inputPrice；有明细时命中按 cacheHitPrice、
//     未命中按 cacheMissPrice（未配=0 则回退 inputPrice，因未命中本质是全价）。
func computeActualCost(p settlementPricing, u usageTokens, requestType string, billingUnits int64) int64 {
	// 倍率未配（零值）视为 1，避免模型漏配 multiplier 时费用被乘成 0（白嫖）。
	mult := p.Multiplier
	if mult <= 0 {
		mult = 1
	}

	var baseCost int64

	if p.PricingMode == "request" || requestType == "image_generation" {
		if billingUnits <= 0 {
			billingUnits = 1
		}
		baseCost = p.RequestPrice * billingUnits
	} else {
		regular := u.Prompt
		hit := 0
		miss := 0
		if u.CacheHit > 0 || u.CacheMiss > 0 {
			hit = u.CacheHit
			miss = u.CacheMiss
			regular = u.Prompt - hit - miss
			if regular < 0 {
				regular = 0
			}
		}
		missPrice := p.CacheMissPrice
		if missPrice <= 0 {
			missPrice = p.InputPrice
		}
		baseCost = (int64(regular)*p.InputPrice +
			int64(hit)*p.CacheHitPrice +
			int64(miss)*missPrice +
			int64(u.Completion)*p.OutputPrice) / 1000000
	}

	return utils.ApplyMultiplier(baseCost, mult, false)
}
