package service

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fingerprintCacheStub struct {
	fingerprint *Fingerprint
}

func (s *fingerprintCacheStub) GetFingerprint(_ context.Context, _ int64) (*Fingerprint, error) {
	return s.fingerprint, nil
}

func (s *fingerprintCacheStub) SetFingerprint(_ context.Context, _ int64, fp *Fingerprint) error {
	copy := *fp
	s.fingerprint = &copy
	return nil
}

func (s *fingerprintCacheStub) GetMaskedSessionID(_ context.Context, _ int64) (string, error) {
	return "", nil
}

func (s *fingerprintCacheStub) SetMaskedSessionID(_ context.Context, _ int64, _ string) error {
	return nil
}

func TestIdentityService_UntrustedClientCannotSeedAccountFingerprint(t *testing.T) {
	cache := &fingerprintCacheStub{}
	svc := NewIdentityService(cache)
	headers := http.Header{
		"User-Agent":                  []string{"curl/9.0.0"},
		"X-Stainless-Os":              []string{"attacker-os"},
		"X-Stainless-Runtime-Version": []string{"attacker-runtime"},
	}

	fp, err := svc.GetOrCreateFingerprint(context.Background(), 42, headers)

	require.NoError(t, err)
	require.Equal(t, defaultFingerprint.UserAgent, fp.UserAgent)
	require.Equal(t, defaultFingerprint.StainlessOS, fp.StainlessOS)
	require.Equal(t, defaultFingerprint.StainlessRuntimeVersion, fp.StainlessRuntimeVersion)
}

func TestIdentityService_ValidatedClaudeClientSeedsAccountFingerprint(t *testing.T) {
	cache := &fingerprintCacheStub{}
	svc := NewIdentityService(cache)
	headers := http.Header{
		"User-Agent":                  []string{"claude-cli/2.1.220 (external, cli)"},
		"X-Stainless-Os":              []string{"MacOS"},
		"X-Stainless-Arch":            []string{"arm64"},
		"X-Stainless-Runtime-Version": []string{"v22.14.0"},
	}

	fp, err := svc.GetOrCreateFingerprint(context.Background(), 42, headers)

	require.NoError(t, err)
	require.Equal(t, "claude-cli/2.1.220 (external, cli)", fp.UserAgent)
	require.Equal(t, "MacOS", fp.StainlessOS)
	require.Equal(t, "arm64", fp.StainlessArch)
	require.Equal(t, "v22.14.0", fp.StainlessRuntimeVersion)
}

func TestIdentityService_OversizedFingerprintMetadataFallsBackToDefault(t *testing.T) {
	cache := &fingerprintCacheStub{}
	svc := NewIdentityService(cache)
	headers := http.Header{
		"User-Agent":     []string{"claude-cli/2.1.220 (external, cli)"},
		"X-Stainless-Os": []string{strings.Repeat("x", maxFingerprintHeaderValueBytes+1)},
	}

	fp, err := svc.GetOrCreateFingerprint(context.Background(), 42, headers)

	require.NoError(t, err)
	require.Equal(t, defaultFingerprint.StainlessOS, fp.StainlessOS)
}
