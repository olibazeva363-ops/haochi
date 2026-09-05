//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestConvertClaudeSKRejectsInvalidConvertURL(t *testing.T) {
	for _, endpoint := range []string{"/convert", "ftp://converter.example/convert", "http://:8080/convert"} {
		t.Run(endpoint, func(t *testing.T) {
			t.Setenv("SUB2API_CONVERT_URL", endpoint)
			_, err := ConvertClaudeSK(context.Background(), "sk-ant-sid02-local", "converter-cookie")
			var convertErr *ClaudeSKConvertError
			if !errors.As(err, &convertErr) || convertErr.Kind != ClaudeSKConvertKindConfiguration {
				t.Fatalf("expected configuration error, got %v", err)
			}
		})
	}
}

func TestConvertClaudeSKFollowsSameOriginRedirect(t *testing.T) {
	var convertedRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/convert", http.StatusTemporaryRedirect)
			return
		}
		convertedRequests.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read converter request: %v", err)
			http.Error(w, "read failed", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost || r.Header.Get("Cookie") != "converter-cookie" || string(body) != `{"cookie":"sk-ant-sid02-local"}` {
			t.Error("same-origin redirect did not preserve the conversion request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh"}`))
	}))
	defer server.Close()
	t.Setenv("SUB2API_CONVERT_URL", server.URL+"/start")

	converted, err := ConvertClaudeSK(context.Background(), "sk-ant-sid02-local", "converter-cookie")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if convertedRequests.Load() != 1 || converted.AccessToken != "access" || converted.RefreshToken != "refresh" {
		t.Fatalf("unexpected converted credentials or request count: %+v, %d", converted, convertedRequests.Load())
	}
}

func TestConvertClaudeSKRejectsCrossOriginRedirect(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var untrustedRequests atomic.Int32
			untrusted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				untrustedRequests.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer untrusted.Close()
			converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, untrusted.URL, status)
			}))
			defer converter.Close()
			t.Setenv("SUB2API_CONVERT_URL", converter.URL)

			_, err := ConvertClaudeSK(context.Background(), "sk-ant-sid02-local", "converter-cookie")
			if err == nil || !strings.Contains(err.Error(), "different origin is not allowed") {
				t.Fatalf("expected blocked redirect, got %v", err)
			}
			if untrustedRequests.Load() != 0 {
				t.Fatal("conversion request reached an untrusted origin")
			}
		})
	}
}

func TestConvertClaudeSKRejectsSchemeChangingRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+r.Host+"/convert", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	t.Setenv("SUB2API_CONVERT_URL", server.URL)

	_, err := ConvertClaudeSK(context.Background(), "sk-ant-sid02-local", "converter-cookie")
	if err == nil || !strings.Contains(err.Error(), "different origin is not allowed") {
		t.Fatalf("expected blocked scheme-changing redirect, got %v", err)
	}
}

func TestConvertClaudeSKLimitsSameOriginRedirects(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "/convert", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	t.Setenv("SUB2API_CONVERT_URL", server.URL)

	_, err := ConvertClaudeSK(context.Background(), "sk-ant-sid02-local", "converter-cookie")
	if err == nil || !strings.Contains(err.Error(), "stopped after 10 converter redirects") {
		t.Fatalf("expected redirect limit error, got %v", err)
	}
	if requests.Load() != 10 {
		t.Fatalf("requests = %d, want 10", requests.Load())
	}
}
