//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPinnedCodexModelsManifestPreservesAstraCapabilitiesThroughPublicAlias(t *testing.T) {
	var calls atomic.Int32
	s := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		calls.Add(1)
		return ordinaryModelsUpstreamResponse(`{"data":[{"id":"gpt-6-astra"},{"id":"gpt-image-2"}]}`), nil
	}})
	account := newCodexModelsAPIKeyTestAccount("https://api.openai.com/v1")
	account.Status, account.Schedulable = StatusActive, true
	account.Credentials["model_mapping"] = map[string]any{"public-astra": "gpt-6-astra"}
	s.accountRepo = splitCodexModelsAccountRepo{all: map[int64][]Account{10: {*account}}}
	group := &Group{
		ID: 10, Platform: PlatformOpenAI,
		CodexModelsManifestConfig: GroupCodexModelsManifestConfig{Enabled: true, AccountIDs: []int64{account.ID}},
		ModelAllowlist:            GroupModelAllowlist{Enabled: true, Models: []string{"public-astra"}},
	}

	manifest, selected, err := s.FetchPinnedCodexModelsManifest(context.Background(), group, CodexCanonicalClientVersion())
	require.NoError(t, err)
	require.Equal(t, account.ID, selected.ID)
	require.NoError(t, s.MergeGroupConfiguredCodexModels(context.Background(), group, manifest, ""))
	var decoded struct {
		Models []configuredCodexModelDescriptor `json:"models"`
	}
	require.NoError(t, json.Unmarshal(manifest.Body, &decoded))
	require.Len(t, decoded.Models, 1)
	model := decoded.Models[0]
	require.Equal(t, "public-astra", model.Slug)
	require.Contains(t, effortsFromConfiguredCodexLevels(model.SupportedReasoningLevels), "ultra")
	require.Equal(t, []string{"text", "image"}, model.InputModalities)
	require.NotNil(t, model.MultiAgentReasoningEffort)
	require.Equal(t, "xhigh", *model.MultiAgentReasoningEffort)
	require.Equal(t, "v2", model.MultiAgentVersion)
	require.True(t, strings.HasPrefix(strings.TrimSpace(model.ModelMessages.InstructionsTemplate), "You are Codex, an agent based on GPT-6."))

	// Group projection must leave the shared upstream catalog available by its original name.
	cached, err := s.FetchCodexModelsManifest(context.Background(), account, CodexCanonicalClientVersion(), "")
	require.NoError(t, err)
	require.Contains(t, string(cached.Body), `"slug":"gpt-6-astra"`)
	require.NotContains(t, string(cached.Body), "public-astra")
	require.EqualValues(t, 1, calls.Load())
}
