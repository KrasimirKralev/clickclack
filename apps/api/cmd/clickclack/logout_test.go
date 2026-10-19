package main

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type logoutRequest struct {
	method        string
	authorization string
	contentType   string
}

func newLogoutServer(t *testing.T, status int) (*httptest.Server, *[]logoutRequest) {
	t.Helper()
	var requests []logoutRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/logout" {
			http.NotFound(w, r)
			return
		}
		requests = append(requests, logoutRequest{method: r.Method, authorization: r.Header.Get("Authorization"), contentType: r.Header.Get("Content-Type")})
		if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "store unavailable"})
		}
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func useTempClientConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"XDG_CONFIG_HOME", "HOME", "AppData"} {
		t.Setenv(name, dir)
	}
	for _, name := range []string{"CLICKCLACK_SERVER", "CLICKCLACK_TOKEN", "CLICKCLACK_USER_ID", "CLICKCLACK_WORKSPACE", "CLICKCLACK_CHANNEL"} {
		t.Setenv(name, "")
	}
	path, err := clientConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func writeStoredClientConfig(t *testing.T, cfg clientConfig) {
	t.Helper()
	if err := saveClientConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestLogoutRevokesStoredSessionBeforeRemovingConfig(t *testing.T) {
	path := useTempClientConfig(t)
	server, requests := newLogoutServer(t, http.StatusOK)
	writeStoredClientConfig(t, clientConfig{Server: server.URL, Token: "ses_stored"})
	t.Setenv("CLICKCLACK_TOKEN", "ses_from_env")

	output := captureStdout(t, func() error { return client([]string{"logout", "--json"}) })

	if len(*requests) != 1 {
		t.Fatalf("expected one logout request, got %d", len(*requests))
	}
	got := (*requests)[0]
	if got.method != http.MethodPost || got.authorization != "Bearer ses_stored" {
		t.Fatalf("unexpected logout request: %+v", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected client config removed, stat err = %v", err)
	}
	if !strings.Contains(output, `"logged_out": true`) {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestLogoutRevokesOnStoredServerWhenServerFlagDiffers(t *testing.T) {
	path := useTempClientConfig(t)
	server, requests := newLogoutServer(t, http.StatusOK)
	other, otherRequests := newLogoutServer(t, http.StatusOK)
	writeStoredClientConfig(t, clientConfig{Server: server.URL, Token: "ses_stored"})

	captureStdout(t, func() error { return client([]string{"logout", "--server", other.URL}) })

	if len(*requests) != 1 || (*requests)[0].authorization != "Bearer ses_stored" {
		t.Fatalf("expected stored session revoked on its own server, got %+v", *requests)
	}
	if len(*otherRequests) != 0 {
		t.Fatalf("stored token sent to a different server: %+v", *otherRequests)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected client config removed, stat err = %v", err)
	}
}

func TestLogoutKeepsConfigWhenRevokeFails(t *testing.T) {
	path := useTempClientConfig(t)
	server, requests := newLogoutServer(t, http.StatusInternalServerError)
	writeStoredClientConfig(t, clientConfig{Server: server.URL, Token: "ses_stored"})

	var err error
	output := captureStdout(t, func() error { err = client([]string{"logout"}); return nil })

	if err == nil || !strings.Contains(err.Error(), "store unavailable") || !strings.Contains(err.Error(), filepath.Base(path)) {
		t.Fatalf("expected revoke error naming the kept config, got %v", err)
	}
	if len(*requests) != 1 {
		t.Fatalf("expected one logout request, got %d", len(*requests))
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("expected client config kept for retry, stat err = %v", statErr)
	}
	if output != "" {
		t.Fatalf("unexpected output on failed logout: %q", output)
	}
}

func TestLogoutWithoutStoredSessionSkipsRevoke(t *testing.T) {
	cases := map[string]*clientConfig{
		"no config":       nil,
		"config no token": {Workspace: "wsp_1"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			path := useTempClientConfig(t)
			server, requests := newLogoutServer(t, http.StatusOK)
			if cfg != nil {
				stored := *cfg
				stored.Server = server.URL
				writeStoredClientConfig(t, stored)
			}

			output := captureStdout(t, func() error { return client([]string{"logout", "--server", server.URL}) })

			if len(*requests) != 0 {
				t.Fatalf("expected no logout request, got %+v", *requests)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected client config absent, stat err = %v", err)
			}
			if output != "logged out\n" {
				t.Fatalf("unexpected output: %q", output)
			}
		})
	}
}
