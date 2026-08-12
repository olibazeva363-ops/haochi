//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
)

func TestConvertClaudeSKRequiresExplicitConvertURL(t *testing.T) {
	t.Setenv("SUB2API_CONVERT_URL", "")

	_, err := ConvertClaudeSK(context.Background(), "sk-ant-sid02-local", "converter-cookie")
	if err == nil {
		t.Fatal("expected error")
	}
	var convertErr *ClaudeSKConvertError
	if !errors.As(err, &convertErr) {
		t.Fatalf("expected ClaudeSKConvertError, got %T", err)
	}
	if convertErr.Kind != ClaudeSKConvertKindConfiguration {
		t.Fatalf("kind = %q, want %q", convertErr.Kind, ClaudeSKConvertKindConfiguration)
	}
	if convertErr.Retryable {
		t.Fatal("configuration error should not be retryable")
	}
}
