package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldRetryUpstreamError_OAuthDoesNotReplayAccessDenial(t *testing.T) {
	svc := &GatewayService{}
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}

	for _, statusCode := range []int{
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			require.False(t, svc.shouldRetryUpstreamError(account, statusCode))
		})
	}
}
