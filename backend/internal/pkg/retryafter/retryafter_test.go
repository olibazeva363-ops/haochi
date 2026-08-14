package retryafter

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseResetTime(t *testing.T) {
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	future := now.Add(90 * time.Second).Format(http.TimeFormat)

	tests := []struct {
		name  string
		value string
		want  time.Time
		ok    bool
	}{
		{name: "integer seconds", value: "30", want: now.Add(30 * time.Second), ok: true},
		{name: "fractional seconds", value: "1.5", want: now.Add(1500 * time.Millisecond), ok: true},
		{name: "http date", value: future, want: now.Add(90 * time.Second), ok: true},
		{name: "zero", value: "0"},
		{name: "negative", value: "-1"},
		{name: "nan", value: "NaN"},
		{name: "positive infinity", value: "+Inf"},
		{name: "duration overflow", value: "9223372036.854776"},
		{name: "past http date", value: now.Add(-time.Minute).Format(http.TimeFormat)},
		{name: "invalid", value: "later"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseResetTime(http.Header{"Retry-After": []string{tt.value}}, now)
			if !tt.ok {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.want, *got)
		})
	}
}

func TestParseResetTimeMissingHeader(t *testing.T) {
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	require.Nil(t, ParseResetTime(nil, now))
	require.Nil(t, ParseResetTime(http.Header{}, now))
}
