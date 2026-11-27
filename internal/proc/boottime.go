package proc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// The platform files define BootTime, which returns when the computer last
// booted as a wall-clock time. Sleep and suspend do not move it, so
// time.Since(BootTime()) is the uptime people mean: the time since the last
// restart, asleep or not.

// parseTimeval decodes the struct timeval that the kern.boottime sysctl
// returns on 64-bit macOS: little-endian int64 seconds, then int32
// microseconds. syscall.Sysctl drops one trailing NUL byte of padding, so 12
// bytes are enough.
func parseTimeval(b []byte) (time.Time, error) {
	if len(b) < 12 {
		return time.Time{}, fmt.Errorf("unexpected %d-byte timeval", len(b))
	}
	sec := int64(binary.LittleEndian.Uint64(b[0:8]))
	usec := int64(binary.LittleEndian.Uint32(b[8:12]))
	if sec <= 0 || usec >= 1_000_000 {
		return time.Time{}, fmt.Errorf("implausible timeval %d.%06d", sec, usec)
	}
	return time.Unix(sec, usec*int64(time.Microsecond)), nil
}

// parseBtime extracts the boot time from the contents of Linux's /proc/stat,
// whose "btime" line holds it in seconds since the Unix epoch.
func parseBtime(stat []byte) (time.Time, error) {
	for _, line := range bytes.Split(stat, []byte("\n")) {
		v, ok := bytes.CutPrefix(line, []byte("btime "))
		if !ok {
			continue
		}
		sec, err := strconv.ParseInt(string(bytes.TrimSpace(v)), 10, 64)
		if err != nil || sec <= 0 {
			return time.Time{}, fmt.Errorf("unexpected btime %q", bytes.TrimSpace(v))
		}
		return time.Unix(sec, 0), nil
	}
	return time.Time{}, errors.New("no btime line")
}
