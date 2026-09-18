package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
)

// The sqlite/postgres members boundary suites drive ListWorkspaceMemberPage on
// the happy path (valid roles, real round-tripped cursors); these pin the
// error/boundary arms that path cannot reach: limit default/clamp/reject,
// invalid role filter, and malformed/stale cursor rejection.

func TestNormalizeWorkspaceMemberPageRequest_ZeroLimitDefaults(t *testing.T) {
	out, err := NormalizeWorkspaceMemberPageRequest(WorkspaceMemberPageRequest{Limit: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Limit != defaultWorkspaceMemberPageLimit {
		t.Errorf("zero limit: got %d, want default %d", out.Limit, defaultWorkspaceMemberPageLimit)
	}
}

func TestNormalizeWorkspaceMemberPageRequest_OverMaxClamps(t *testing.T) {
	out, err := NormalizeWorkspaceMemberPageRequest(WorkspaceMemberPageRequest{Limit: maxWorkspaceMemberPageLimit + 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Limit != maxWorkspaceMemberPageLimit {
		t.Errorf("over-max limit: got %d, want clamp %d", out.Limit, maxWorkspaceMemberPageLimit)
	}
}

func TestNormalizeWorkspaceMemberPageRequest_NegativeLimitRejected(t *testing.T) {
	_, err := NormalizeWorkspaceMemberPageRequest(WorkspaceMemberPageRequest{Limit: -1})
	if !errors.Is(err, ErrInvalidWorkspaceMemberPage) {
		t.Fatalf("negative limit: got err %v, want ErrInvalidWorkspaceMemberPage", err)
	}
}

func TestNormalizeWorkspaceMemberPageRequest_InvalidRoleFilterRejected(t *testing.T) {
	if _, err := NormalizeWorkspaceMemberPageRequest(WorkspaceMemberPageRequest{Limit: 10, Role: ""}); err != nil {
		t.Errorf("empty role filter should pass, got %v", err)
	}
	_, err := NormalizeWorkspaceMemberPageRequest(WorkspaceMemberPageRequest{Limit: 10, Role: "wizard"})
	if !errors.Is(err, ErrInvalidWorkspaceMemberPage) {
		t.Fatalf("invalid role filter: got err %v, want ErrInvalidWorkspaceMemberPage", err)
	}
}

func TestDecodeWorkspaceMemberCursor_MalformedRejected(t *testing.T) {
	notBase64 := "!!!not-base64!!!"
	if _, _, err := DecodeWorkspaceMemberCursor(notBase64); !errors.Is(err, ErrInvalidWorkspaceMemberPage) {
		t.Errorf("bad base64: got err %v, want ErrInvalidWorkspaceMemberPage", err)
	}
	notJSON := base64.RawURLEncoding.EncodeToString([]byte("not json"))
	if _, _, err := DecodeWorkspaceMemberCursor(notJSON); !errors.Is(err, ErrInvalidWorkspaceMemberPage) {
		t.Errorf("bad json: got err %v, want ErrInvalidWorkspaceMemberPage", err)
	}
}

// A cursor from a different schema version, or one missing the tie-break user id,
// must be rejected: both are required for a stable keyset page.
func TestDecodeWorkspaceMemberCursor_VersionAndUserIDRequired(t *testing.T) {
	encode := func(c WorkspaceMemberCursor) string {
		payload, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(payload)
	}
	wrongVersion := encode(WorkspaceMemberCursor{Version: workspaceMemberCursorVersion + 1, UserID: "u1"})
	if _, _, err := DecodeWorkspaceMemberCursor(wrongVersion); !errors.Is(err, ErrInvalidWorkspaceMemberPage) {
		t.Errorf("wrong version: got err %v, want ErrInvalidWorkspaceMemberPage", err)
	}
	missingUser := encode(WorkspaceMemberCursor{Version: workspaceMemberCursorVersion, UserID: ""})
	if _, _, err := DecodeWorkspaceMemberCursor(missingUser); !errors.Is(err, ErrInvalidWorkspaceMemberPage) {
		t.Errorf("missing user id: got err %v, want ErrInvalidWorkspaceMemberPage", err)
	}
}

// WorkspaceMemberRoleSort is the keyset page's primary ordering: owner first,
// then moderator, member, bot, guest, and any unknown role sorted last so a
// role that outlives this map never jumps ahead of a known one.
func TestWorkspaceMemberRoleSort_OrdersKnownRolesAndSinksUnknown(t *testing.T) {
	for _, tc := range []struct {
		role string
		want int
	}{
		{WorkspaceRoleOwner, 0},
		{WorkspaceRoleModerator, 1},
		{WorkspaceRoleMember, 2},
		{WorkspaceRoleBot, 3},
		{WorkspaceRoleGuest, 4},
	} {
		if got := WorkspaceMemberRoleSort(tc.role); got != tc.want {
			t.Errorf("WorkspaceMemberRoleSort(%q) = %d, want %d", tc.role, got, tc.want)
		}
	}

	unknown := WorkspaceMemberRoleSort("wizard")
	if unknown <= WorkspaceMemberRoleSort(WorkspaceRoleGuest) {
		t.Errorf("unknown role sort %d must be greater than every known role", unknown)
	}
}

// Set routes a per-role count onto the matching field and touches no other, so
// a role tally can be assembled field by field; an unknown role is a no-op.
func TestWorkspaceMemberRoleCounts_SetRoutesEachRole(t *testing.T) {
	field := func(c WorkspaceMemberRoleCounts, role string) int {
		switch role {
		case WorkspaceRoleOwner:
			return c.Owner
		case WorkspaceRoleModerator:
			return c.Moderator
		case WorkspaceRoleMember:
			return c.Member
		case WorkspaceRoleBot:
			return c.Bot
		case WorkspaceRoleGuest:
			return c.Guest
		}
		return -1
	}

	roles := []string{
		WorkspaceRoleOwner, WorkspaceRoleModerator, WorkspaceRoleMember,
		WorkspaceRoleBot, WorkspaceRoleGuest,
	}
	for _, role := range roles {
		var counts WorkspaceMemberRoleCounts
		counts.Set(role, 7)
		if got := field(counts, role); got != 7 {
			t.Errorf("Set(%q, 7) did not land on its own field: got %d", role, got)
		}
		// Every other role's field must stay zero — no cross-assignment.
		total := counts.Owner + counts.Moderator + counts.Member + counts.Bot + counts.Guest
		if total != 7 {
			t.Errorf("Set(%q, 7) touched another field: total = %d, want 7", role, total)
		}
	}

	var counts WorkspaceMemberRoleCounts
	counts.Set("wizard", 5)
	if (counts != WorkspaceMemberRoleCounts{}) {
		t.Errorf("Set on unknown role must be a no-op, got %+v", counts)
	}
}
