package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/openclaw/clickclack/apps/api/internal/store"
)

type threadRequest struct {
	method, path, query, body string
}

type threadServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []threadRequest
}

func newThreadServer(t *testing.T) *threadServer {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	fake := &threadServer{}
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		fake.mu.Lock()
		fake.requests = append(fake.requests, threadRequest{r.Method, r.URL.Path, r.URL.RawQuery, payload["body"]})
		fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/messages/msg_1/thread":
			_ = json.NewEncoder(w).Encode(store.ThreadPage{Root: store.Message{ID: "msg_1", Body: "root"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/messages/msg_1/thread/replies":
			_ = json.NewEncoder(w).Encode(map[string]any{"message": store.Message{ID: "msg_2", Body: payload["body"]}})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	t.Cleanup(fake.Close)
	return fake
}

func (s *threadServer) recorded() []threadRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]threadRequest(nil), s.requests...)
}

func TestThreadCommandsAcceptFlagsBeforeMessageID(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		want   threadRequest
		output string
	}{
		{
			name:   "open with json after id",
			args:   []string{"threads", "open", "msg_1", "--json"},
			want:   threadRequest{http.MethodGet, "/api/messages/msg_1/thread", "limit=100", ""},
			output: `"id": "msg_1"`,
		},
		{
			name:   "open with json before id",
			args:   []string{"threads", "open", "--json", "msg_1"},
			want:   threadRequest{http.MethodGet, "/api/messages/msg_1/thread", "limit=100", ""},
			output: `"id": "msg_1"`,
		},
		{
			name:   "open with valued flag before id",
			args:   []string{"thread", "open", "--limit", "1", "msg_1"},
			want:   threadRequest{http.MethodGet, "/api/messages/msg_1/thread", "limit=1", ""},
			output: "msg_1\troot\n",
		},
		{
			name:   "open with flags on both sides of id",
			args:   []string{"threads", "open", "--json", "msg_1", "--limit", "1"},
			want:   threadRequest{http.MethodGet, "/api/messages/msg_1/thread", "limit=1", ""},
			output: `"id": "msg_1"`,
		},
		{
			name:   "reply with json after id",
			args:   []string{"threads", "reply", "msg_1", "--json", "hello", "there"},
			want:   threadRequest{http.MethodPost, "/api/messages/msg_1/thread/replies", "", "hello there"},
			output: `"id": "msg_2"`,
		},
		{
			name:   "reply with json before id",
			args:   []string{"threads", "reply", "--json", "msg_1", "hello", "there"},
			want:   threadRequest{http.MethodPost, "/api/messages/msg_1/thread/replies", "", "hello there"},
			output: `"id": "msg_2"`,
		},
		{
			name:   "reply with body flag before id",
			args:   []string{"threads", "reply", "--body", "from flag", "msg_1"},
			want:   threadRequest{http.MethodPost, "/api/messages/msg_1/thread/replies", "", "from flag"},
			output: "replied msg_2 to msg_1\n",
		},
		{
			name:   "top-level reply with plain before id",
			args:   []string{"reply", "--plain", "msg_1", "hello"},
			want:   threadRequest{http.MethodPost, "/api/messages/msg_1/thread/replies", "", "hello"},
			output: "msg_2\n",
		},
		{
			name:   "flags after the body stay body text",
			args:   []string{"reply", "msg_1", "use", "--json"},
			want:   threadRequest{http.MethodPost, "/api/messages/msg_1/thread/replies", "", "use --json"},
			output: "replied msg_2 to msg_1\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := newThreadServer(t)
			args := append([]string{"--server", server.URL, "--user", "usr_test"}, tc.args...)
			output := captureStdout(t, func() error { return client(args) })
			requests := server.recorded()
			if len(requests) != 1 || requests[0] != tc.want {
				t.Fatalf("requests = %+v, want [%+v]", requests, tc.want)
			}
			if !strings.Contains(output, tc.output) {
				t.Fatalf("output = %q, want containing %q", output, tc.output)
			}
		})
	}
}

func TestThreadCommandsRequireMessageIDBeforeNetworkRequest(t *testing.T) {
	for _, args := range [][]string{
		{"threads", "open", "--json"},
		{"threads", "reply", "--json"},
		{"reply", "--plain"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			server := newThreadServer(t)
			err := client(append([]string{"--server", server.URL, "--user", "usr_test"}, args...))
			if err == nil || !strings.Contains(err.Error(), "message ID is required") {
				t.Fatalf("error = %v, want message ID is required", err)
			}
			if requests := server.recorded(); len(requests) != 0 {
				t.Fatalf("requests = %+v, want none", requests)
			}
		})
	}
}
