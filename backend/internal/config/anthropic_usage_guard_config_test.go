package config

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestAnthropicUsageGuardDefaultIsGentle(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	setDefaults()
	require.Equal(t, 97, viper.GetInt("rate_limit.anthropic_usage_pause_threshold_percent"))
}
