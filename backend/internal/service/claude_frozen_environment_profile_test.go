package service

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type frozenEnvironmentAccountRepo struct {
	AccountRepository
	mu          sync.Mutex
	updates     []map[string]any
	pauses      []string
	updateErr   error
	pauseErr    error
	updateCalls int
	pauseCalls  int
}

func (r *frozenEnvironmentAccountRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateCalls++
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updates = append(r.updates, updates)
	return nil
}

func (r *frozenEnvironmentAccountRepo) SetTempUnschedulable(_ context.Context, _ int64, _ time.Time, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pauseCalls++
	if r.pauseErr != nil {
		return r.pauseErr
	}
	r.pauses = append(r.pauses, reason)
	return nil
}

func TestClaudeFrozenTransportRetriesFailedPersistence(t *testing.T) {
	for _, failPause := range []bool{true, false} {
		name := "profile_write_failed"
		if failPause {
			name = "pause_write_failed"
		}
		t.Run(name, func(t *testing.T) {
			oldProxy, newProxy := int64(11), int64(22)
			account := &Account{ID: 43, Platform: PlatformAnthropic, Type: AccountTypeOAuth, ProxyID: &oldProxy}
			profile := newClaudeFrozenEnvironmentProfile(account, nil)
			account.ProxyID = &newProxy
			repo := &frozenEnvironmentAccountRepo{}
			if failPause {
				repo.pauseErr = context.DeadlineExceeded
			} else {
				repo.updateErr = context.DeadlineExceeded
			}
			svc := &GatewayService{accountRepo: repo}
			svc.claudeFrozenProfiles.Store(account.ID, profile)

			svc.observeClaudeFrozenTransport(context.Background(), account, profile)
			cached, _ := svc.claudeFrozenProfiles.Load(account.ID)
			require.Same(t, profile, cached, "failed persistence must not acknowledge the proxy change")
			if failPause {
				require.Zero(t, repo.updateCalls, "do not persist a new proxy before saving its pause")
			}
			require.Len(t, repo.updates, 0)

			repo.pauseErr, repo.updateErr = nil, nil
			svc.observeClaudeFrozenTransport(context.Background(), account, profile)
			cached, _ = svc.claudeFrozenProfiles.Load(account.ID)
			require.NotSame(t, profile, cached)
			updated, ok := cached.(*ClaudeFrozenEnvironmentProfile)
			require.True(t, ok)
			require.Equal(t, newProxy, updated.ProxyID)
			require.Equal(t, oldProxy, profile.ProxyID)
			require.Equal(t, 2, repo.pauseCalls)
			require.Len(t, repo.updates, 1)
		})
	}
}

func TestClaudeFrozenTransportUpdatesWithoutRepository(t *testing.T) {
	oldProxy, newProxy := int64(11), int64(22)
	account := &Account{ID: 44, Platform: PlatformAnthropic, Type: AccountTypeOAuth, ProxyID: &oldProxy}
	profile := newClaudeFrozenEnvironmentProfile(account, nil)
	account.ProxyID = &newProxy
	svc := &GatewayService{}
	svc.claudeFrozenProfiles.Store(account.ID, profile)

	svc.observeClaudeFrozenTransport(context.Background(), account, profile)
	cached, _ := svc.claudeFrozenProfiles.Load(account.ID)
	updated, ok := cached.(*ClaudeFrozenEnvironmentProfile)
	require.True(t, ok)
	require.Equal(t, newProxy, updated.ProxyID)
	require.Equal(t, oldProxy, profile.ProxyID)
	svc.observeClaudeFrozenTransport(context.Background(), account, updated)
	cached, _ = svc.claudeFrozenProfiles.Load(account.ID)
	require.Same(t, updated, cached, "an unchanged proxy keeps the same immutable snapshot")
}

func TestClaudeFrozenTransportConcurrentObservationsKeepSnapshotsImmutable(t *testing.T) {
	oldProxy, newProxy := int64(11), int64(22)
	oldAccount := &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeOAuth, ProxyID: &oldProxy}
	profile := newClaudeFrozenEnvironmentProfile(oldAccount, nil)
	account := *oldAccount
	account.ProxyID = &newProxy
	repo := &frozenEnvironmentAccountRepo{}
	svc := &GatewayService{accountRepo: repo}
	svc.claudeFrozenProfiles.Store(account.ID, profile)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			svc.observeClaudeFrozenTransport(context.Background(), &account, profile)
		}()
	}
	close(start)
	wg.Wait()

	require.Equal(t, oldProxy, profile.ProxyID, "in-flight requests keep an immutable old snapshot")
	cached, ok := svc.claudeFrozenProfiles.Load(account.ID)
	require.True(t, ok)
	updated, ok := cached.(*ClaudeFrozenEnvironmentProfile)
	require.True(t, ok)
	require.NotSame(t, profile, updated)
	require.Equal(t, newProxy, updated.ProxyID)
	require.Equal(t, profile.DeviceID, updated.DeviceID)
	require.Equal(t, profile.UserAgent, updated.UserAgent)
	require.Len(t, repo.updates, 1, "one proxy change must be persisted once")
	require.Len(t, repo.pauses, 1)
	require.Contains(t, repo.pauses[0], "11 -> 22")
	require.Same(t, updated, repo.updates[0][claudeFrozenEnvironmentProfileExtraKey])

	// A late request carrying the old account and profile cannot undo the update.
	svc.observeClaudeFrozenTransport(context.Background(), oldAccount, profile)
	cached, _ = svc.claudeFrozenProfiles.Load(account.ID)
	require.Same(t, updated, cached)
	require.Len(t, repo.updates, 1)
	require.Len(t, repo.pauses, 1)
}

func TestClaudeFrozenEnvironmentProfileRoundTrip(t *testing.T) {
	account := &Account{
		ID:       42,
		Platform: PlatformAnthropic,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": -1,
		},
	}
	profile := newClaudeFrozenEnvironmentProfile(account, &tlsfingerprint.Profile{Name: "random-at-creation"})

	require.NoError(t, validateClaudeFrozenEnvironmentProfile(profile))
	require.Len(t, profile.ClientID, 64)
	require.Len(t, profile.DeviceID, 64)
	require.NotEqual(t, profile.ClientID, profile.DeviceID)
	require.Equal(t, int64(0), profile.TLSFingerprintProfileID, "random TLS mode must be pinned")
	require.Equal(t, "random-at-creation", profile.TLSFingerprint.Name)
	recreated := newClaudeFrozenEnvironmentProfile(account, nil)
	require.Equal(t, profile.ClientID, recreated.ClientID)
	require.Equal(t, profile.DeviceID, recreated.DeviceID)

	decoded, err := decodeClaudeFrozenEnvironmentProfile(profile)
	require.NoError(t, err)
	require.Equal(t, profile, decoded)
}

func TestRewriteClaudeFrozenMetadataWithoutAccountUUID(t *testing.T) {
	service := &GatewayService{identityService: &IdentityService{}}
	account := &Account{ID: 9, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	profile := newClaudeFrozenEnvironmentProfile(account, nil)
	body := []byte(`{"metadata":{"user_id":"{\"device_id\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"account_uuid\":\"\",\"session_id\":\"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa\"}"}}`)

	rewritten := service.rewriteClaudeFrozenMetadata(context.Background(), body, account, profile)
	parsed := ParseMetadataUserID(gjson.GetBytes(rewritten, "metadata.user_id").String())
	require.NotNil(t, parsed)
	require.Equal(t, profile.DeviceID, parsed.DeviceID)
	require.NotEqual(t, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", parsed.SessionID)
}

func TestGetOrCreateClaudeFrozenEnvironmentProfilePersistsOnce(t *testing.T) {
	repo := &frozenEnvironmentAccountRepo{}
	service := &GatewayService{accountRepo: repo}
	account := &Account{ID: 7, Platform: PlatformAnthropic, Type: AccountTypeOAuth}

	first, err := service.getOrCreateClaudeFrozenEnvironmentProfile(context.Background(), account)
	require.NoError(t, err)
	second, err := service.getOrCreateClaudeFrozenEnvironmentProfile(context.Background(), account)
	require.NoError(t, err)

	require.Same(t, first, second)
	require.Len(t, repo.updates, 1)
	require.Same(t, first, repo.updates[0][claudeFrozenEnvironmentProfileExtraKey])
}

func TestFinalizeClaudeFrozenMimicRequestRebuildsHeaders(t *testing.T) {
	service := &GatewayService{identityService: &IdentityService{}}
	profile := &ClaudeFrozenEnvironmentProfile{
		Schema:                  claudeFrozenEnvironmentProfileSchema,
		Source:                  "simulated",
		ClientID:                "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DeviceID:                "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		UserAgent:               "claude-cli/2.1.100 (external, cli)",
		StainlessLang:           "js",
		StainlessPackageVersion: "0.94.0",
		StainlessOS:             "Linux",
		StainlessArch:           "x64",
		StainlessRuntime:        "node",
		StainlessRuntimeVersion: "v24.3.0",
		BetaSet:                 []string{"oauth-2025-04-20"},
		FrozenAt:                time.Now().UTC(),
	}
	req, err := http.NewRequest(http.MethodPost, claudeAPIURL, nil)
	require.NoError(t, err)
	req.Header.Set("authorization", "Bearer TOKEN")
	req.Header.Set("traceparent", "dirty")
	req.Header.Set("x-unrelated-client-header", "dirty")
	req.Header.Set("User-Agent", "untrusted-client/9.9.9")

	err = service.finalizeClaudeFrozenMimicRequest(req, profile, true, "oauth-2025-04-20", true)
	require.NoError(t, err)
	require.Equal(t, "Bearer TOKEN", getHeaderRaw(req.Header, "authorization"))
	require.Equal(t, profile.UserAgent, getHeaderRaw(req.Header, "User-Agent"))
	require.Equal(t, profile.StainlessOS, getHeaderRaw(req.Header, "X-Stainless-OS"))
	require.Equal(t, "oauth-2025-04-20", getHeaderRaw(req.Header, "anthropic-beta"))
	require.Equal(t, "stream", getHeaderRaw(req.Header, "x-stainless-helper-method"))
	require.Empty(t, getHeaderRaw(req.Header, "traceparent"))
	require.Empty(t, getHeaderRaw(req.Header, "x-unrelated-client-header"))
	require.NotEmpty(t, getHeaderRaw(req.Header, "x-client-request-id"))
}

func TestTLSProfileForRequestPrefersFrozenBinding(t *testing.T) {
	fallback := &tlsfingerprint.Profile{Name: "fallback"}
	frozen := &tlsfingerprint.Profile{Name: "frozen"}
	req, err := http.NewRequest(http.MethodPost, claudeAPIURL, nil)
	require.NoError(t, err)

	require.Same(t, fallback, tlsProfileForRequest(req, fallback))
	req = attachClaudeFrozenTLSProfile(req, frozen)
	require.Same(t, frozen, tlsProfileForRequest(req, fallback))
}
