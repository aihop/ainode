package utils

import "encoding/json"

type RequestPricingConfig struct {
	RequestPriceCents int64 `json:"request_price_cents"`
}

func ParseRequestPricingConfig(raw []byte) RequestPricingConfig {
	if len(raw) == 0 {
		return RequestPricingConfig{}
	}

	var cfg RequestPricingConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return RequestPricingConfig{}
	}
	return cfg
}
