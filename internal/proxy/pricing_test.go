package proxy

import "testing"

// 价格均为「每 1M tokens、10^8 放大」的整数；token 计费 baseCost = tokens*price/1e6。
func TestComputeActualCost_TokenMode(t *testing.T) {
	const M = 1_000_000

	cases := []struct {
		name        string
		pricing     settlementPricing
		usage       usageTokens
		requestType string
		want        int64
	}{
		{
			name:    "no cache info -> prompt billed at inputPrice (not cacheMiss=0)",
			pricing: settlementPricing{InputPrice: 100, CacheMissPrice: 0, Multiplier: 1},
			usage:   usageTokens{Prompt: M}, // 无缓存明细
			want:    100,                    // 1M*100/1e6
		},
		{
			name:    "cache split with explicit cacheMissPrice",
			pricing: settlementPricing{InputPrice: 100, CacheHitPrice: 10, CacheMissPrice: 100, Multiplier: 1},
			usage:   usageTokens{Prompt: M, CacheHit: 400_000, CacheMiss: 600_000},
			want:    64, // (400k*10 + 600k*100)/1e6
		},
		{
			name:    "cacheMissPrice unset falls back to inputPrice",
			pricing: settlementPricing{InputPrice: 100, CacheHitPrice: 10, CacheMissPrice: 0, Multiplier: 1},
			usage:   usageTokens{Prompt: M, CacheHit: 400_000, CacheMiss: 600_000},
			want:    64, // 与上一条相同：miss 回退 100
		},
		{
			name:    "completion priced at outputPrice",
			pricing: settlementPricing{InputPrice: 100, OutputPrice: 300, Multiplier: 1},
			usage:   usageTokens{Prompt: 0, Completion: M},
			want:    300,
		},
		{
			name:    "multiplier applied (round)",
			pricing: settlementPricing{InputPrice: 100, Multiplier: 1.5},
			usage:   usageTokens{Prompt: M},
			want:    150, // 100 * 1.5
		},
		{
			name:    "cache tokens exceed prompt -> regular floored at 0",
			pricing: settlementPricing{InputPrice: 100, CacheHitPrice: 10, CacheMissPrice: 100, Multiplier: 1},
			usage:   usageTokens{Prompt: 500_000, CacheHit: 400_000, CacheMiss: 600_000},
			want:    64, // regular=max(0,-100k)=0；(400k*10+600k*100)/1e6
		},
		{
			name:    "zero multiplier treated as 1",
			pricing: settlementPricing{InputPrice: 100, Multiplier: 0},
			usage:   usageTokens{Prompt: M},
			want:    100,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeActualCost(c.pricing, c.usage, c.requestType, 0)
			if got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestComputeActualCost_RequestMode(t *testing.T) {
	p := settlementPricing{PricingMode: "request", RequestPrice: 200, Multiplier: 1}

	if got := computeActualCost(p, usageTokens{}, "", 3); got != 600 {
		t.Fatalf("request mode x3 = %d, want 600", got)
	}
	// billingUnits <= 0 视为 1
	if got := computeActualCost(p, usageTokens{}, "", 0); got != 200 {
		t.Fatalf("request mode default unit = %d, want 200", got)
	}
	// 倍率
	p2 := settlementPricing{PricingMode: "request", RequestPrice: 200, Multiplier: 1.5}
	if got := computeActualCost(p2, usageTokens{}, "", 2); got != 600 {
		t.Fatalf("request mode x2 *1.5 = %d, want 600", got)
	}
}

func TestComputeActualCost_ImageGenerationUsesRequestPrice(t *testing.T) {
	// pricingMode 为 token，但 requestType=image_generation 也走按次价
	p := settlementPricing{PricingMode: "token", RequestPrice: 250, InputPrice: 999, Multiplier: 1}
	if got := computeActualCost(p, usageTokens{Prompt: 1_000_000}, "image_generation", 2); got != 500 {
		t.Fatalf("image generation = %d, want 500 (250*2, 忽略 token 价)", got)
	}
}
