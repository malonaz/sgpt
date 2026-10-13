// Package format renders numbers the same way everywhere in the TUI.
package format

import (
	"fmt"
	"time"
)

// Price renders a dollar amount: cents precision, with sub-cent spend shown
// as such rather than as a misleading $0.00.
func Price(dollars float64) string {
	switch {
	case dollars == 0:
		return "$0"
	case dollars < 0.01:
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", dollars)
}

// Tokens renders a token count compactly: 950, 4.3k, 1.2m.
func Tokens(count int64) string {
	switch {
	case count < 1000:
		return fmt.Sprintf("%d", count)
	case count < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(count)/1000)
	}
	return fmt.Sprintf("%.1fm", float64(count)/1_000_000)
}

// Duration renders an elapsed time at the precision a human reads it: 8s,
// 1m 12s, 1h 5m.
func Duration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

// Age renders how long ago t was: now, 5m, 3h, 2d, 4mo, 1y.
func Age(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	}
	return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
}
