package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildUpstreamTransportHasBoundedConnectTimeouts(t *testing.T) {
	transport, err := buildUpstreamTransport(defaultPoolSettings(nil), nil, upstreamProtocolModeDefault)
	require.NoError(t, err)
	require.NotNil(t, transport.DialContext)
	require.Equal(t, defaultTLSHandshakeTimeout, transport.TLSHandshakeTimeout)
}
