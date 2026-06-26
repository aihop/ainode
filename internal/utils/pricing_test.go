package utils

import "testing"

func TestApplyMultiplier(t *testing.T) {
	cases := []struct {
		name    string
		base    int64
		mult    float32
		roundUp bool
		want    int64
	}{
		{"zero base returns zero", 0, 2.0, true, 0},
		{"negative base returns zero", -100, 2.0, false, 0},
		{"multiplier 1.0 keeps amount (round)", 137, 1.0, false, 137},
		{"multiplier 1.0 keeps amount (ceil)", 137, 1.0, true, 137},
		{"prededuct rounds up fractional", 10, 1.04, true, 11}, // 10.4 -> ceil 11
		{"settle rounds down fractional", 10, 1.04, false, 10}, // 10.4 -> round 10
		{"settle rounds up above half", 10, 1.06, false, 11},   // 10.6 -> round 11
		{"prededuct ceils exact-ish", 200, 1.5, true, 300},
		{"settle on exact integer", 200, 1.5, false, 300},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ApplyMultiplier(c.base, c.mult, c.roundUp)
			if got != c.want {
				t.Fatalf("ApplyMultiplier(%d, %v, %v) = %d, want %d", c.base, c.mult, c.roundUp, got, c.want)
			}
		})
	}
}

// TestApplyMultiplierCeilNeverUnderEstimates 保证预扣（向上取整）永远 >= 结算（四舍五入），
// 否则会出现预扣不足、用户透支的风险。
func TestApplyMultiplierCeilNeverUnderEstimates(t *testing.T) {
	mults := []float32{1.0, 1.01, 1.1, 1.333, 1.5, 2.0, 3.7}
	bases := []int64{1, 7, 10, 99, 1000, 999999}
	for _, m := range mults {
		for _, b := range bases {
			ceil := ApplyMultiplier(b, m, true)
			round := ApplyMultiplier(b, m, false)
			if ceil < round {
				t.Fatalf("ceil(%d*%v)=%d < round=%d, 预扣不应低于结算", b, m, ceil, round)
			}
		}
	}
}
