package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestUnknownOrRevokedBearerGetsASignInAgainError(t *testing.T) {
	fixture := newRealtimeSessionFixture(t, "ended-session@example.com", 0)
	ctx := context.Background()
	revoked, err := fixture.store.CreateSession(ctx, fixture.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.RevokeSession(ctx, revoked.Token); err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{"revoked": revoked.Token, "never issued": "sst_never_issued"} {
		req, err := http.NewRequest(http.MethodGet, fixture.http.URL+"/api/me", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Error string `json:"error"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s bearer returned HTTP %d, want 401", name, resp.StatusCode)
		}
		if strings.Contains(body.Error, "sql") || !strings.Contains(body.Error, "sign in again") {
			t.Errorf("%s bearer error = %q, want a sign-in-again message without database text", name, body.Error)
		}
	}
}
