package worker

import "testing"

func TestSplitActualDeduction(t *testing.T) {
	cases := []struct {
		name          string
		actualCost    int64
		grantDeducted int64
		wantGrant     int64
		wantCash      int64
	}{
		{"fully within grant", 40, 100, 40, 0},
		{"exactly equals grant", 100, 100, 100, 0},
		{"spills to cash", 120, 100, 100, 20},
		{"no grant deducted -> all cash", 50, 0, 0, 50},
		{"zero cost", 0, 100, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, cash := splitActualDeduction(c.actualCost, c.grantDeducted)
			if g != c.wantGrant || cash != c.wantCash {
				t.Fatalf("split(%d,%d) = grant %d/cash %d, want %d/%d",
					c.actualCost, c.grantDeducted, g, cash, c.wantGrant, c.wantCash)
			}
		})
	}
}
