//go:build !darwin && !linux

package proc

import (
	"fmt"
	"runtime"
	"time"
)

// BootTime is not implemented on this platform.
func BootTime() (time.Time, error) {
	return time.Time{}, fmt.Errorf("reading the boot time is not supported on %s", runtime.GOOS)
}
