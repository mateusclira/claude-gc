//go:build darwin

package proc

import (
	"fmt"
	"syscall"
	"time"
)

// BootTime returns when the computer last booted, from the kern.boottime
// sysctl.
func BootTime() (time.Time, error) {
	raw, err := syscall.Sysctl("kern.boottime")
	if err != nil {
		return time.Time{}, fmt.Errorf("reading kern.boottime: %w", err)
	}
	t, err := parseTimeval([]byte(raw))
	if err != nil {
		return time.Time{}, fmt.Errorf("reading kern.boottime: %w", err)
	}
	return t, nil
}
