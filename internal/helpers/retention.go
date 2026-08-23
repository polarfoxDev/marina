package helpers

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/polarfoxDev/marina/internal/model"
)

// "7d:14w:6m" -> daily:weekly:monthly
func ParseRetention(s string) model.Retention {
	var r model.Retention
	if s == "" {
		return model.Retention{KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6}
	}
	parts := strings.Split(s, ":")
	parse := func(p string) int {
		if p == "" {
			return 0
		}
		// strip suffix
		digits := p
		if n := len(p); n > 0 {
			switch p[n-1] {
			case 'd', 'w', 'm', 'y':
				digits = p[:n-1]
			}
		}
		n, _ := strconv.Atoi(digits)
		return n
	}
	if len(parts) > 0 {
		r.KeepDaily = parse(parts[0])
	}
	if len(parts) > 1 {
		r.KeepWeekly = parse(parts[1])
	}
	if len(parts) > 2 {
		r.KeepMonthly = parse(parts[2])
	}
	if r.KeepDaily == 0 && r.KeepWeekly == 0 && r.KeepMonthly == 0 {
		return model.Retention{KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6}
	}
	return r
}

// SelectExpiredSnapshots decides which of the given snapshot timestamps are
// expired under retention r, given the current set of snapshots (there is
// no external clock involved -- the policy is purely "which of the
// snapshots I already have should I keep").
//
// The newest snapshot in each of the most recent r.KeepDaily distinct
// calendar days, r.KeepWeekly distinct ISO weeks, and r.KeepMonthly distinct
// months is kept; buckets are independent, so a snapshot kept by the daily
// rule and one kept by the weekly rule can be different snapshots. The
// single newest snapshot overall is always kept, even under a "0:0:0"
// policy -- retention should never be able to delete every backup.
//
// The returned slice (possibly empty, never for the newest snapshot) is in
// no particular order.
func SelectExpiredSnapshots(snapshots []time.Time, r model.Retention) []time.Time {
	if len(snapshots) == 0 {
		return nil
	}

	sorted := append([]time.Time(nil), snapshots...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].After(sorted[j]) })

	keep := make([]bool, len(sorted))
	keep[0] = true // newest snapshot is always kept

	keepMostRecentPerBucket := func(n int, bucketKey func(time.Time) string) {
		seen := map[string]bool{}
		for i, t := range sorted {
			if len(seen) >= n {
				break
			}
			key := bucketKey(t)
			if !seen[key] {
				seen[key] = true
				keep[i] = true
			}
		}
	}

	keepMostRecentPerBucket(r.KeepDaily, func(t time.Time) string {
		return t.Format("2006-01-02")
	})
	keepMostRecentPerBucket(r.KeepWeekly, func(t time.Time) string {
		year, week := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", year, week)
	})
	keepMostRecentPerBucket(r.KeepMonthly, func(t time.Time) string {
		return t.Format("2006-01")
	})

	var expired []time.Time
	for i, t := range sorted {
		if !keep[i] {
			expired = append(expired, t)
		}
	}
	return expired
}
