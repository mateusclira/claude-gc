//go:build linux

package proc

import (
	"fmt"
	"os"
	"time"
)

// BootTime returns when the computer last booted, from the btime field of
// /proc/stat.
func BootTime() (time.Time, error) {
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, fmt.Errorf("reading the boot time: %w", err)
	}
	t, err := parseBtime(stat)
	if err != nil {
		return time.Time{}, fmt.Errorf("reading the boot time from /proc/stat: %w", err)
	}
	return t, nil
}
