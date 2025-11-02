package helpers

import (
	"strconv"
	"strings"

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
