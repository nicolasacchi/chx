package output

import (
	"fmt"
	"strconv"
	"time"
)

// FormatFunc transforms a raw value into a display string.
type FormatFunc func(value any) string

// FormatEpochSeconds — render integer seconds-since-epoch as local timestamp.
func FormatEpochSeconds(v any) string {
	n, ok := toInt64(v)
	if !ok || n == 0 {
		return ""
	}
	return time.Unix(n, 0).Local().Format("2006-01-02 15:04:05")
}

// FormatBytes — render integer byte counts as human-readable (KiB/MiB/GiB).
func FormatBytes(v any) string {
	n, ok := toInt64(v)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	const (
		KiB = 1024
		MiB = 1024 * KiB
		GiB = 1024 * MiB
		TiB = 1024 * GiB
	)
	switch {
	case n >= TiB:
		return fmt.Sprintf("%.1fT", float64(n)/TiB)
	case n >= GiB:
		return fmt.Sprintf("%.1fG", float64(n)/GiB)
	case n >= MiB:
		return fmt.Sprintf("%.1fM", float64(n)/MiB)
	case n >= KiB:
		return fmt.Sprintf("%.1fK", float64(n)/KiB)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// Truncate returns a FormatFunc that caps strings at n chars (with ellipsis).
func Truncate(n int) FormatFunc {
	return func(v any) string {
		s := fmt.Sprintf("%v", v)
		if len(s) <= n {
			return s
		}
		if n <= 1 {
			return s[:n]
		}
		return s[:n-1] + "…"
	}
}

func toInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int:
		return int64(x), true
	case float64:
		return int64(x), true
	case string:
		if n, err := strconv.ParseInt(x, 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}
