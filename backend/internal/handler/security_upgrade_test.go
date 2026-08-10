package handler

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPendingOAuthCompletionMayApplyAdoption(t *testing.T) {
	require.False(t, pendingOAuthCompletionMayApplyAdoption(false, "choose_account_action_required"))
	require.True(t, pendingOAuthCompletionMayApplyAdoption(true, "choose_account_action_required"))
	require.True(t, pendingOAuthCompletionMayApplyAdoption(false, oauthIntentBindCurrentUser))
}

func TestValidateAPIKeyRequestsRejectInvalidLimits(t *testing.T) {
	nan := math.NaN()
	negative := -1.0
	zeroDays := 0
	require.Error(t, validateAPIKeyCreateRequest(CreateAPIKeyRequest{Quota: &nan}))
	require.Error(t, validateAPIKeyCreateRequest(CreateAPIKeyRequest{RateLimit5h: &negative}))
	require.Error(t, validateAPIKeyCreateRequest(CreateAPIKeyRequest{ExpiresInDays: &zeroDays}))
	require.NoError(t, validateAPIKeyCreateRequest(CreateAPIKeyRequest{}))
	require.Error(t, validateAPIKeyUpdateRequest(UpdateAPIKeyRequest{RateLimit7d: &nan}))
}
