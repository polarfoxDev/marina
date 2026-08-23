package helpers

import (
	"testing"
	"time"

	"github.com/polarfoxDev/marina/internal/model"
)

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

func TestSelectExpiredSnapshots(t *testing.T) {
	day := func(offset int) time.Time {
		return time.Date(2026, 8, 23, 2, 0, 0, 0, time.UTC).AddDate(0, 0, -offset)
	}
	within := func(times []time.Time, t2 time.Time) bool {
		for _, t := range times {
			if t.Equal(t2) {
				return true
			}
		}
		return false
	}

	t.Run("empty input", func(t *testing.T) {
		expired := SelectExpiredSnapshots(nil, model.Retention{KeepDaily: 7})
		if len(expired) != 0 {
			t.Fatalf("expected no expired snapshots for empty input, got %v", expired)
		}
	})

	t.Run("fewer snapshots than policy keeps everything", func(t *testing.T) {
		snaps := []time.Time{day(0), day(1), day(2)}
		expired := SelectExpiredSnapshots(snaps, model.Retention{KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6})
		if len(expired) != 0 {
			t.Fatalf("expected nothing expired, got %v", expired)
		}
	})

	t.Run("several snapshots in one day only keep the newest of that day", func(t *testing.T) {
		base := day(0)
		snaps := []time.Time{
			base,
			base.Add(-1 * time.Hour),
			base.Add(-2 * time.Hour),
			day(1),
		}
		expired := SelectExpiredSnapshots(snaps, model.Retention{KeepDaily: 2})
		if within(expired, base) || within(expired, day(1)) {
			t.Fatalf("newest snapshot of each kept day should not be expired: %v", expired)
		}
		if !within(expired, base.Add(-1*time.Hour)) || !within(expired, base.Add(-2*time.Hour)) {
			t.Fatalf("extra same-day snapshots should be expired: %v", expired)
		}
	})

	t.Run("policy of 0:0:0 still keeps the newest snapshot", func(t *testing.T) {
		snaps := []time.Time{day(0), day(1), day(2)}
		expired := SelectExpiredSnapshots(snaps, model.Retention{})
		if within(expired, day(0)) {
			t.Fatalf("newest snapshot must never be expired, got %v", expired)
		}
		if len(expired) != 2 {
			t.Fatalf("expected the two older snapshots expired, got %v", expired)
		}
	})

	t.Run("daily bucket keeps one per day across many days", func(t *testing.T) {
		var snaps []time.Time
		for i := 0; i < 10; i++ {
			snaps = append(snaps, day(i))
		}
		expired := SelectExpiredSnapshots(snaps, model.Retention{KeepDaily: 5})
		if len(expired) != 5 {
			t.Fatalf("expected 5 expired (10 days - 5 kept), got %d: %v", len(expired), expired)
		}
		for i := 0; i < 5; i++ {
			if within(expired, day(i)) {
				t.Fatalf("day %d should be kept, was expired", i)
			}
		}
		for i := 5; i < 10; i++ {
			if !within(expired, day(i)) {
				t.Fatalf("day %d should be expired, was kept", i)
			}
		}
	})
}
