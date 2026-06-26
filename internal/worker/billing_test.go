package worker

import "testing"

func TestSplitActual3(t *testing.T) {
	cases := []struct {
		name                   string
		actual, sp, gr, ca     int64
		wantSP, wantGR, wantCA int64
	}{
		{"within sub", 40, 100, 50, 50, 40, 0, 0},
		{"cascade to grant", 80, 30, 100, 50, 30, 50, 0},
		{"cascade through all", 80, 30, 30, 100, 30, 30, 20},
		{"no sub -> grant then cash", 80, 0, 30, 100, 0, 30, 50},
		{"all cash", 50, 0, 0, 100, 0, 0, 50},
		{"zero cost", 0, 100, 100, 100, 0, 0, 0},
		{"exactly sub", 30, 30, 50, 50, 30, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sp, gr, ca := splitActual3(c.actual, c.sp, c.gr, c.ca)
			if sp != c.wantSP || gr != c.wantGR || ca != c.wantCA {
				t.Fatalf("split3(%d, sp=%d gr=%d ca=%d) = %d/%d/%d, want %d/%d/%d",
					c.actual, c.sp, c.gr, c.ca, sp, gr, ca, c.wantSP, c.wantGR, c.wantCA)
			}
		})
	}
}
