package helpers

import "testing"

func TestParseRetention(t *testing.T) {
	cases := []struct {
		in   string
		expD int
		expW int
		expM int
	}{
		{"", 7, 4, 6},
		{"7d:14w:6m", 7, 14, 6},
		{"10:5:1", 10, 5, 1},
		{"0:0:0", 7, 4, 6}, // all zero -> default
		{"5d::", 5, 0, 0},
		{"5:2", 5, 2, 0},
	}
	for _, c := range cases {
		r := ParseRetention(c.in)
		if r.KeepDaily != c.expD || r.KeepWeekly != c.expW || r.KeepMonthly != c.expM {
			// display failing input
			// r is model.Retention; fields exported
			if r.KeepDaily != c.expD || r.KeepWeekly != c.expW || r.KeepMonthly != c.expM {
				// redundant check to satisfy linter for clarity
			}

			// Use t.Errorf for aggregated details
			t.Errorf("ParseRetention(%q) => %v (d=%d w=%d m=%d), want d=%d w=%d m=%d", c.in, r, r.KeepDaily, r.KeepWeekly, r.KeepMonthly, c.expD, c.expW, c.expM)
		}
	}
}
