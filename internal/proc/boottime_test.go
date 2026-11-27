package proc

import (
	"encoding/binary"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseTimeval(t *testing.T) {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint64(b[0:8], 1788698984)
	binary.LittleEndian.PutUint32(b[8:12], 842815)
	want := time.Unix(1788698984, 842815000)
	for _, n := range []int{16, 15, 12} { // full struct, NUL trimmed, minimum
		got, err := parseTimeval(b[:n])
		if err != nil {
			t.Fatalf("%d bytes: %v", n, err)
		}
		if !got.Equal(want) {
			t.Errorf("%d bytes: got %v, want %v", n, got, want)
		}
	}
	if _, err := parseTimeval(b[:8]); err == nil {
		t.Error("short timeval accepted")
	}
	if _, err := parseTimeval(make([]byte, 16)); err == nil {
		t.Error("zero timeval accepted")
	}
	binary.LittleEndian.PutUint32(b[8:12], 2_000_000)
	if _, err := parseTimeval(b); err == nil {
		t.Error("out of range microseconds accepted")
	}
}

func TestParseBtime(t *testing.T) {
	stat := "cpu  10132153 290696 3084719 46828483 16683 0 25195 0 0 0\n" +
		"cpu0 1393280 32966 572056 13343292 6130 0 17875 0 0 0\n" +
		"intr 1462898 0 0\nctxt 115315133\nbtime 1788698984\nprocesses 86031\n"
	got, err := parseBtime([]byte(stat))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(time.Unix(1788698984, 0)) {
		t.Errorf("got %v", got)
	}
	for _, bad := range []string{"cpu 1 2 3\n", "btime soon\n", "btime 0\n", ""} {
		if _, err := parseBtime([]byte(bad)); err == nil {
			t.Errorf("parseBtime(%q) accepted", bad)
		}
	}
}

// TestBootTimeLive checks the real boot time against an independent source.
func TestBootTimeLive(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("boot time is only implemented on macOS and Linux")
	}
	boot, err := BootTime()
	if err != nil {
		t.Fatal(err)
	}
	if !boot.Before(time.Now()) || boot.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("implausible boot time %v", boot)
	}
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "kern.boottime").Output()
		if err != nil {
			t.Skipf("sysctl: %v", err)
		}
		// "{ sec = 1788698984, usec = 842815 } Sun Sep  6 09:49:44 2026"
		m := regexp.MustCompile(`\{ sec = (\d+)`).FindSubmatch(out)
		if m == nil {
			t.Fatalf("unexpected sysctl output %q", out)
		}
		sec, _ := strconv.ParseInt(string(m[1]), 10, 64)
		if boot.Unix() != sec {
			t.Errorf("BootTime %d != sysctl %d", boot.Unix(), sec)
		}
	case "linux":
		data, err := os.ReadFile("/proc/uptime")
		if err != nil {
			t.Skipf("/proc/uptime: %v", err)
		}
		up, err := strconv.ParseFloat(strings.Fields(string(data))[0], 64)
		if err != nil {
			t.Fatal(err)
		}
		fromUptime := time.Now().Add(-time.Duration(up * float64(time.Second)))
		if d := boot.Sub(fromUptime); d > 5*time.Second || d < -5*time.Second {
			t.Errorf("BootTime %v differs from /proc/uptime by %v", boot, d)
		}
	}
}
