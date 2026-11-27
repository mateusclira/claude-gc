package humanize

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"3d", 72 * time.Hour, true},
		{"72h", 72 * time.Hour, true},
		{"1d12h", 36 * time.Hour, true},
		{"1.5d", 36 * time.Hour, true},
		{"30m", 30 * time.Minute, true},
		{"45s", 45 * time.Second, true},
		{"1h30m", 90 * time.Minute, true},
		{" 2d ", 48 * time.Hour, true},
		{"0", 0, true},
		{"0s", 0, true},
		{"", 0, false},
		{"3", 0, false},
		{"d", 0, false},
		{"3x", 0, false},
		{"-1d", 0, false},
	}
	for _, c := range cases {
		got, err := ParseDuration(c.in)
		if c.ok && err != nil {
			t.Errorf("ParseDuration(%q) unexpected error: %v", c.in, err)
			continue
		}
		if !c.ok && err == nil {
			t.Errorf("ParseDuration(%q) expected error, got %v", c.in, got)
			continue
		}
		if got != c.want {
			t.Errorf("ParseDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDuration(t *testing.T) {
	cases := map[time.Duration]string{
		65*Day + 2*time.Hour + 7*time.Minute: "65d 2h",
		3 * Day:                              "3d",
		3*time.Hour + 12*time.Minute:         "3h 12m",
		2 * time.Hour:                        "2h",
		45 * time.Minute:                     "45m",
		12 * time.Second:                     "12s",
		-12 * time.Second:                    "12s",
	}
	for in, want := range cases {
		if got := Duration(in); got != want {
			t.Errorf("Duration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestAgo(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	if got := Ago(now.Add(-5*time.Second), now); got != "just now" {
		t.Errorf("Ago = %q", got)
	}
	if got := Ago(now.Add(-2*time.Hour), now); got != "2h ago" {
		t.Errorf("Ago = %q", got)
	}
	if got := Ago(time.Time{}, now); got != "unknown" {
		t.Errorf("Ago(zero) = %q", got)
	}
}

func TestBytes(t *testing.T) {
	cases := map[int64]string{
		512:                      "512 B",
		284032 * 1024:            "277 MB",
		8993 * 1024 * 1024:       "8.8 GB",
		150 * 1024 * 1024 * 1024: "150 GB",
	}
	for in, want := range cases {
		if got := Bytes(in); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", in, got, want)
		}
	}
	if got := KB(284032); got != "277 MB" {
		t.Errorf("KB = %q", got)
	}
}

func TestPlural(t *testing.T) {
	if got := Plural(1, "session"); got != "1 session" {
		t.Errorf("Plural = %q", got)
	}
	if got := Plural(3, "session"); got != "3 sessions" {
		t.Errorf("Plural = %q", got)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		3 * Day:                             "3d",
		2 * Day:                             "2d",
		24 * time.Hour:                      "24h",
		36 * time.Hour:                      "36h",
		30 * time.Minute:                    "30m",
		5 * time.Second:                     "5s",
		90 * time.Second:                    "1m30s",
		0:                                   "0s",
		500 * time.Millisecond:              "500ms",
		2*Day + 3*time.Hour + 4*time.Second: "2d3h4s",
	}
	for in, want := range cases {
		got := FormatDuration(in)
		if got != want {
			t.Errorf("FormatDuration(%v) = %q, want %q", in, got, want)
			continue
		}
		if in >= time.Second {
			back, err := ParseDuration(got)
			if err != nil || back != in {
				t.Errorf("round trip %q: %v %v", got, back, err)
			}
		}
	}
}

func TestSpan(t *testing.T) {
	cases := map[time.Duration]string{
		time.Hour:        "1 hour",
		24 * time.Hour:   "24 hours",
		48 * time.Hour:   "48 hours",
		72 * time.Hour:   "3d",
		90 * time.Minute: "1h 30m",
		30 * time.Minute: "30m",
	}
	for in, want := range cases {
		if got := Span(in); got != want {
			t.Errorf("Span(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestUntil(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	if got := Until(now.Add(21*time.Hour+58*time.Minute), now); got != "in 21h 58m" {
		t.Errorf("Until = %q", got)
	}
	if got := Until(now.Add(10*time.Second), now); got != "now" {
		t.Errorf("Until(soon) = %q", got)
	}
	if got := Until(now.Add(-time.Hour), now); got != "now" {
		t.Errorf("Until(past) = %q", got)
	}
	if got := Until(time.Time{}, now); got != "unknown" {
		t.Errorf("Until(zero) = %q", got)
	}
}
