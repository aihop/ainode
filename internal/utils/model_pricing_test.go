package utils

import "testing"

func TestParseRequestPricingConfig(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{"empty bytes -> zero", "", 0},
		{"valid config", `{"request_price_cents":250}`, 250},
		{"missing field -> zero", `{"other":1}`, 0},
		{"invalid json -> zero", `{not-json`, 0},
		{"null -> zero", `null`, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseRequestPricingConfig([]byte(c.raw)).RequestPriceCents
			if got != c.want {
				t.Fatalf("ParseRequestPricingConfig(%q).RequestPriceCents = %d, want %d", c.raw, got, c.want)
			}
		})
	}
}
