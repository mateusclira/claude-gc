// Package humanize parses and formats durations and sizes for people.
package humanize

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Day is the length of a day as a time.Duration.
const Day = 24 * time.Hour

// ParseDuration parses durations such as "3d", "36h", "1d12h", "90m" or "45s".
// It accepts every unit time.ParseDuration accepts, plus "d" for days.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty duration")
	}
	var total time.Duration
	rest := s
	for rest != "" {
		i := 0
		for i < len(rest) && (isDigit(rest[i]) || rest[i] == '.') {
			i++
		}
		if i == 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		num, rest2 := rest[:i], rest[i:]
		j := 0
		for j < len(rest2) && !isDigit(rest2[j]) && rest2[j] != '.' {
			j++
		}
		if j == 0 {
			return 0, fmt.Errorf("invalid duration %q: missing unit (try 3d, 72h or 30m)", s)
		}
		unit, next := rest2[:j], rest2[j:]
		if unit == "d" {
			f, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid duration %q", s)
			}
			total += time.Duration(f * float64(Day))
		} else {
			d, err := time.ParseDuration(num + unit)
			if err != nil {
				return 0, fmt.Errorf("invalid duration %q", s)
			}
			total += d
		}
		rest = next
	}
	return total, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// Duration renders d compactly: "65d 2h", "3h 12m", "45m", "12s".
func Duration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d >= Day:
		days, hours := d/Day, (d%Day)/time.Hour
		if hours == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd %dh", days, hours)
	case d >= time.Hour:
		hours, mins := d/time.Hour, (d%time.Hour)/time.Minute
		if mins == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh %dm", hours, mins)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return fmt.Sprintf("%ds", d/time.Second)
	}
}

// Ago renders how long before now t happened: "2h 5m ago" or "just now".
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := now.Sub(t)
	if d < 30*time.Second {
		return "just now"
	}
	return Duration(d) + " ago"
}

// Bytes renders a byte count using binary units: "278 MB", "8.8 GB".
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value, exp := float64(n), 0
	for value >= unit && exp < 4 {
		value /= unit
		exp++
	}
	suffix := []string{"B", "KB", "MB", "GB", "TB"}[exp]
	if value >= 100 || exp < 3 {
		return fmt.Sprintf("%.0f %s", value, suffix)
	}
	return fmt.Sprintf("%.1f %s", value, suffix)
}

// KB renders a size given in kibibytes, the unit ps(1) reports RSS in.
func KB(kb int64) string { return Bytes(kb * 1024) }

// Plural returns "n word" or "n words".
func Plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// FormatDuration renders d in the compact form ParseDuration accepts:
// "3d", "1d12h", "30m", "5s".
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Second {
		return d.String()
	}
	var b strings.Builder
	for _, part := range []struct {
		unit time.Duration
		name string
	}{{Day, "d"}, {time.Hour, "h"}, {time.Minute, "m"}, {time.Second, "s"}} {
		if n := d / part.unit; n > 0 {
			fmt.Fprintf(&b, "%d%s", n, part.name)
			d -= n * part.unit
		}
	}
	return b.String()
}
