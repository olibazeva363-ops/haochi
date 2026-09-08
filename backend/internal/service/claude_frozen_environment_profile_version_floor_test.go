//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeFrozenVersionFloorPreservesAccountEnvironment(t *testing.T) {
	for _, source := range []string{"database", "cache"} {
		t.Run(source, func(t *testing.T) {
			account := &Account{ID: 87, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
			profile := newClaudeFrozenEnvironmentProfile(account, &tlsfingerprint.Profile{Name: "pinned"})
			profile.UserAgent = "claude-cli/2.1.220 (external, cli)"
			profile.StainlessPackageVersion = "0.94.0"
			profile.ProxyID = 12
			profile.ProxyFingerprint = "original-proxy"
			account.Extra = map[string]any{claudeFrozenEnvironmentProfileExtraKey: profile}
			repo := &frozenEnvironmentAccountRepo{}
			svc := &GatewayService{accountRepo: repo}
			if source == "cache" {
				svc.claudeFrozenProfiles.Store(account.ID, profile)
			}

			updated, err := svc.getOrCreateClaudeFrozenEnvironmentProfile(context.Background(), account)
			require.NoError(t, err)
			expected := *profile
			expected.UserAgent = "claude-cli/" + claude.CLICurrentVersion + " (external, cli)"
			require.Equal(t, &expected, updated, "only the CLI version may change")
			require.Equal(t, "claude-cli/2.1.220 (external, cli)", profile.UserAgent)
			require.Len(t, repo.updates, 1)
			require.Same(t, updated, repo.updates[0][claudeFrozenEnvironmentProfileExtraKey])

			again, err := svc.getOrCreateClaudeFrozenEnvironmentProfile(context.Background(), account)
			require.NoError(t, err)
			require.Same(t, updated, again, "the stale account snapshot must not restore the old version")
			require.Len(t, repo.updates, 1)
		})
	}
}

func TestClaudeFrozenVersionFloorDoesNotDowngrade(t *testing.T) {
	for _, version := range []string{claude.CLICurrentVersion, "2.99.999"} {
		t.Run(version, func(t *testing.T) {
			account := &Account{ID: 88, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
			profile := newClaudeFrozenEnvironmentProfile(account, nil)
			profile.UserAgent = "claude-cli/" + version + " (external, cli)"
			account.Extra = map[string]any{claudeFrozenEnvironmentProfileExtraKey: profile}
			repo := &frozenEnvironmentAccountRepo{}
			svc := &GatewayService{accountRepo: repo}

			unchanged, err := svc.getOrCreateClaudeFrozenEnvironmentProfile(context.Background(), account)
			require.NoError(t, err)
			require.Equal(t, profile, unchanged)
			require.Zero(t, repo.updateCalls)
		})
	}
}

func TestClaudeFrozenVersionFloorRetriesFailedPersistence(t *testing.T) {
	for _, source := range []string{"database", "cache"} {
		t.Run(source, func(t *testing.T) {
			account := &Account{ID: 89, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
			profile := newClaudeFrozenEnvironmentProfile(account, nil)
			profile.UserAgent = "claude-cli/2.1.220 (external, cli)"
			account.Extra = map[string]any{claudeFrozenEnvironmentProfileExtraKey: profile}
			repo := &frozenEnvironmentAccountRepo{updateErr: context.DeadlineExceeded}
			svc := &GatewayService{accountRepo: repo}
			if source == "cache" {
				svc.claudeFrozenProfiles.Store(account.ID, profile)
			}

			_, err := svc.getOrCreateClaudeFrozenEnvironmentProfile(context.Background(), account)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			cached, exists := svc.claudeFrozenProfiles.Load(account.ID)
			if source == "cache" {
				require.True(t, exists)
				require.Same(t, profile, cached)
			} else {
				require.False(t, exists, "a failed update must not be cached as completed")
			}
			repo.updateErr = nil
			updated, err := svc.getOrCreateClaudeFrozenEnvironmentProfile(context.Background(), account)
			require.NoError(t, err)
			require.Equal(t, claude.CLICurrentVersion, ExtractCLIVersion(updated.UserAgent))
			require.Equal(t, 2, repo.updateCalls)
			require.Len(t, repo.updates, 1)
		})
	}
}

func TestClaudeFrozenVersionFloorConcurrentLoadsPreserveProxyUpdate(t *testing.T) {
	oldProxy, newProxy := int64(11), int64(22)
	account := &Account{ID: 90, Platform: PlatformAnthropic, Type: AccountTypeOAuth, ProxyID: &oldProxy}
	profile := newClaudeFrozenEnvironmentProfile(account, nil)
	profile.UserAgent = "claude-cli/2.1.220 (external, cli)"
	account.Extra = map[string]any{claudeFrozenEnvironmentProfileExtraKey: profile}
	repo := &frozenEnvironmentAccountRepo{}
	svc := &GatewayService{accountRepo: repo}
	svc.claudeFrozenProfiles.Store(account.ID, profile)
	movedAccount := *account
	movedAccount.ProxyID = &newProxy
	svc.observeClaudeFrozenTransport(context.Background(), &movedAccount, profile)

	const concurrency = 32
	profiles := make([]*ClaudeFrozenEnvironmentProfile, concurrency)
	errors := make([]error, concurrency)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			profiles[i], errors[i] = svc.getOrCreateClaudeFrozenEnvironmentProfile(context.Background(), account)
		}()
	}
	close(start)
	wg.Wait()

	for i := range concurrency {
		require.NoError(t, errors[i])
		require.Same(t, profiles[0], profiles[i])
		require.Equal(t, newProxy, profiles[i].ProxyID)
		require.Equal(t, claude.CLICurrentVersion, ExtractCLIVersion(profiles[i].UserAgent))
	}
	require.Equal(t, oldProxy, profile.ProxyID)
	require.Equal(t, "claude-cli/2.1.220 (external, cli)", profile.UserAgent)
	require.Len(t, repo.updates, 2, "one proxy write and one version write")
	require.Len(t, repo.pauses, 1, "a CLI version upgrade does not pause the account")
	svc.observeClaudeFrozenTransport(context.Background(), account, profile)
	cached, _ := svc.claudeFrozenProfiles.Load(account.ID)
	require.Same(t, profiles[0], cached, "a late transport observation cannot undo the version or proxy update")
}

func TestClaudeFrozenVersionFloorMatchesWireBillingVersion(t *testing.T) {
	account := &Account{ID: 91, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	profile := newClaudeFrozenEnvironmentProfile(account, nil)
	profile.UserAgent = "claude-cli/2.1.220 (external, cli)"
	profile.StainlessPackageVersion = "0.94.0"
	account.Extra = map[string]any{claudeFrozenEnvironmentProfileExtraKey: profile}
	repo := &frozenEnvironmentAccountRepo{}
	svc := &GatewayService{accountRepo: repo, identityService: &IdentityService{}, cfg: &config.Config{}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.99.999 (external, cli)")
	body := []byte(`{"model":"claude-fable-5-1","system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.220.abc; cc_entrypoint=cli;"}],"messages":[{"role":"user","content":"version check"}]}`)

	req, wireBody, err := svc.buildUpstreamRequest(context.Background(), c, account, body, "token", "oauth", "claude-fable-5-1", false, true)
	require.NoError(t, err)
	defer func() { require.NoError(t, req.Body.Close()) }()
	actualBody, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, wireBody, actualBody)
	require.Equal(t, "claude-cli/"+claude.CLICurrentVersion+" (external, cli)", getHeaderRaw(req.Header, "User-Agent"))
	require.Equal(t, "0.94.0", getHeaderRaw(req.Header, "X-Stainless-Package-Version"))
	billingText := gjson.GetBytes(actualBody, "system.0.text").String()
	require.Contains(t, billingText, "cc_version="+claude.CLICurrentVersion+".")
	require.NotContains(t, billingText, "2.1.220")
	require.Len(t, repo.updates, 1)
}
