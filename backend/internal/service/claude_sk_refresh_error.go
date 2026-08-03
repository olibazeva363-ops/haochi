package service

import (
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

func isClaudeSKConvertPermanentError(err error) bool {
	var convertErr *ClaudeSKConvertError
	return errors.As(err, &convertErr) && !convertErr.Retryable
}

func isClaudeSKAccountPermanentError(err error) bool {
	var convertErr *ClaudeSKConvertError
	if !errors.As(err, &convertErr) {
		return false
	}
	switch convertErr.Kind {
	case ClaudeSKConvertKindSourceSKInvalid,
		ClaudeSKConvertKindAccountBlocked,
		ClaudeSKConvertKindSubscription,
		ClaudeSKConvertKindRequestMalformed:
		return true
	default:
		return false
	}
}

func isClaudeSKConverterEnvironmentError(err error) bool {
	var convertErr *ClaudeSKConvertError
	if !errors.As(err, &convertErr) {
		return false
	}
	switch convertErr.Kind {
	case ClaudeSKConvertKindCookieInvalid,
		ClaudeSKConvertKindCloudflare,
		ClaudeSKConvertKindConfiguration:
		return true
	default:
		return false
	}
}

func refreshErrorStatusMessage(err error) string {
	var convertErr *ClaudeSKConvertError
	if errors.As(err, &convertErr) {
		return "SK 自动转换失败: " + logredact.RedactText(convertErr.UserMessage()) + " [" + convertErr.Kind + "]"
	}
	return "Token refresh failed (non-retryable): " + logredact.RedactText(err.Error())
}
