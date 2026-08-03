package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

func (s *GatewayService) recoverClaudeSKImportTokenOn401(ctx context.Context, account *Account, responseBody []byte) (string, map[string]any, error) {
	if s == nil || s.accountRepo == nil {
		return "", nil, errors.New("account repository is not configured")
	}
	if !IsClaudeSKImportAccount(account) {
		return "", nil, errors.New("account is not a Claude SK import account")
	}
	upstreamMsg := extractUpstreamErrorMessage(responseBody)
	if upstreamMsg != "" && !looksLikeTokenAuthenticationFailure(upstreamMsg) {
		return "", nil, errors.New("401 does not look like token expiration or revocation")
	}

	converted, err := ConvertClaudeSKFromEnv(ctx, account.GetCredential(ClaudeSKCookieCredentialKey))
	if err != nil {
		var convertErr *ClaudeSKConvertError
		if errors.As(err, &convertErr) {
			slog.Warn("gateway.sk_import_401_recovery_convert_failed",
				"account_id", account.ID,
				"kind", convertErr.Kind,
				"retryable", convertErr.Retryable,
				"needs_cookie_refresh", convertErr.NeedsCookieRefresh,
				"error", logredact.RedactText(convertErr.UserMessage()),
			)
		}
		if isClaudeSKAccountPermanentError(err) {
			errorMsg := refreshErrorStatusMessage(err)
			if setErr := s.accountRepo.SetError(ctx, account.ID, errorMsg); setErr != nil {
				slog.Warn("gateway.sk_import_401_recovery_set_error_failed", "account_id", account.ID, "error", setErr)
			} else {
				slog.Info("gateway.sk_import_401_recovery_set_error", "account_id", account.ID, "reason", logredact.RedactText(errorMsg))
			}
		} else if isClaudeSKConverterEnvironmentError(err) {
			until := time.Now().Add(10 * time.Minute)
			reason := "SK auto-refresh blocked by converter environment: " + logredact.RedactText(refreshErrorStatusMessage(err))
			if setErr := s.accountRepo.SetTempUnschedulable(ctx, account.ID, until, reason); setErr != nil {
				slog.Warn("gateway.sk_import_401_recovery_set_temp_failed", "account_id", account.ID, "error", setErr)
			}
		}
		return "", nil, err
	}

	newCredentials := BuildClaudeCredentialsFromConverted(converted)
	newCredentials["_token_version"] = time.Now().UnixMilli()
	merged := MergeCredentials(account.Credentials, newCredentials)
	if err := persistAccountCredentials(ctx, s.accountRepo, account, merged); err != nil {
		return "", nil, err
	}

	if s.claudeTokenProvider != nil && s.claudeTokenProvider.tokenCache != nil {
		if err := s.claudeTokenProvider.tokenCache.DeleteAccessToken(ctx, ClaudeTokenCacheKey(account)); err != nil {
			slog.Warn("gateway.sk_import_401_recovery_cache_delete_failed", "account_id", account.ID, "error", err)
		}
	}
	if s.accountRepo != nil {
		if err := s.accountRepo.ClearTempUnschedulable(ctx, account.ID); err != nil {
			slog.Warn("gateway.sk_import_401_recovery_clear_temp_failed", "account_id", account.ID, "error", err)
		}
		if account.Status == StatusError {
			if err := s.accountRepo.ClearError(ctx, account.ID); err != nil {
				slog.Warn("gateway.sk_import_401_recovery_clear_error_failed", "account_id", account.ID, "error", err)
			}
		}
		if !account.Schedulable {
			if err := s.accountRepo.SetSchedulable(ctx, account.ID, true); err != nil {
				slog.Warn("gateway.sk_import_401_recovery_schedulable_failed", "account_id", account.ID, "error", err)
			}
		}
	}
	if s.rateLimitService != nil {
		s.rateLimitService.notifyAccountSchedulingBlockCleared(account.ID)
	}

	return converted.AccessToken, merged, nil
}

func looksLikeTokenAuthenticationFailure(message string) bool {
	upstreamMsg := strings.ToLower(message)
	return strings.Contains(upstreamMsg, "token") ||
		strings.Contains(upstreamMsg, "oauth") ||
		strings.Contains(upstreamMsg, "unauthorized") ||
		strings.Contains(upstreamMsg, "authentication") ||
		strings.Contains(upstreamMsg, "revoked") ||
		strings.Contains(upstreamMsg, "expired")
}
