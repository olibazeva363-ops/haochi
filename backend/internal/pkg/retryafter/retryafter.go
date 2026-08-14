package retryafter

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ParseResetTime parses Retry-After as either delay seconds or an HTTP date.
// Invalid, non-positive, and duration-overflowing values are ignored so callers
// can apply their normal bounded fallback policy.
func ParseResetTime(headers http.Header, now time.Time) *time.Time {
	if headers == nil {
		return nil
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return nil
	}

	if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
		maxSeconds := float64(math.MaxInt64) / float64(time.Second)
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > maxSeconds {
			return nil
		}
		resetAt := now.Add(time.Duration(seconds * float64(time.Second)))
		if !resetAt.After(now) {
			return nil
		}
		return &resetAt
	}

	parsed, err := http.ParseTime(raw)
	if err != nil || !parsed.After(now) {
		return nil
	}
	return &parsed
}
