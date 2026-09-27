package users

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/viewdock/viewdock/internal/auth"
)

func (f *fixture) callAs(t *testing.T, p *auth.Principal, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// joinByInvite puts member into owner's household through the consent flow.
func (f *fixture) joinByInvite(t *testing.T, owner, member auth.User, ageLimit int) {
	t.Helper()
	code, inv := f.call(t, owner, http.MethodPost, "/household/invites", map[string]any{"role": "child", "age_limit": ageLimit})
	if code != http.StatusCreated {
		t.Fatalf("invite %d %v", code, inv)
	}
	if code, body := f.call(t, member, http.MethodPost, "/household/invites/accept", map[string]string{"token": inv["token"].(string)}); code != http.StatusOK {
		t.Fatalf("accept %d %v", code, body)
	}
}

func householdID(t *testing.T, body map[string]any) string {
	t.Helper()
	h, ok := body["household"].(map[string]any)
	if !ok {
		t.Fatalf("no household in %v", body)
	}
	return h["id"].(string)
}

func (f *fixture) countMemberships(t *testing.T, userID string) int {
	t.Helper()
	var n int
	if err := f.api.DB.QueryRow(`SELECT COUNT(*) FROM household_members WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestHouseholdAdminRequiresUsersManage(t *testing.T) {
	f := newFixture(t)
	_, home := f.call(t, f.owner, http.MethodPost, "/household", map[string]string{"name": "Home"})
	hid := householdID(t, home)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/admin/households"},
		{http.MethodGet, "/admin/households/" + hid},
		{http.MethodPatch, "/admin/households/" + hid},
		{http.MethodDelete, "/admin/households/" + hid},
		{http.MethodPost, "/admin/households/" + hid + "/invites"},
		{http.MethodPatch, "/admin/households/" + hid + "/members/" + f.other.ID},
		{http.MethodDelete, "/admin/households/" + hid + "/members/" + f.other.ID},
	} {
		if code, _ := f.call(t, f.owner, tc.method, tc.path, map[string]any{}); code != http.StatusForbidden {
			t.Errorf("household owner reached %s %s: %d", tc.method, tc.path, code)
		}
	}
	if code, _ := f.call(t, f.admin, http.MethodGet, "/admin/households", nil); code != http.StatusOK {
		t.Fatalf("admin list %d", code)
	}
}

func TestHouseholdAdminLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	third, err := f.api.Auth.CreateUser(ctx, "third", "third-pass-123", "Third", false)
	if err != nil {
		t.Fatal(err)
	}
	_, home := f.call(t, f.owner, http.MethodPost, "/household", map[string]string{"name": "Home"})
	homeID := householdID(t, home)
	f.joinByInvite(t, f.owner, f.other, 12)

	code, created := f.call(t, f.admin, http.MethodPost, "/admin/households", map[string]any{"name": "Flat", "owner_id": third.ID})
	if code != http.StatusCreated {
		t.Fatalf("admin create %d %v", code, created)
	}
	flatID := householdID(t, created)
	if code, _ := f.call(t, f.admin, http.MethodPost, "/admin/households", map[string]any{"name": "Dup", "owner_id": f.other.ID}); code != http.StatusConflict {
		t.Fatalf("second household for a member: %d", code)
	}

	code, list := f.call(t, f.admin, http.MethodGet, "/admin/households", nil)
	if code != http.StatusOK || len(list["items"].([]any)) != 2 {
		t.Fatalf("list %d %v", code, list)
	}

	// IDOR: a member of Home cannot be edited through Flat's URL.
	if code, _ := f.call(t, f.admin, http.MethodPatch, "/admin/households/"+flatID+"/members/"+f.other.ID, map[string]any{"age_limit": 7}); code != http.StatusNotFound {
		t.Fatalf("cross-household member edit: %d", code)
	}
	if code, _ := f.call(t, f.admin, http.MethodPatch, "/admin/households/"+homeID+"/members/"+f.other.ID, map[string]any{"role": "owner"}); code != http.StatusBadRequest {
		t.Fatalf("owner role assigned: %d", code)
	}
	if code, _ := f.call(t, f.admin, http.MethodPatch, "/admin/households/"+homeID+"/members/"+f.other.ID, map[string]any{"age_limit": 99}); code != http.StatusBadRequest {
		t.Fatalf("out of range age limit: %d", code)
	}
	if code, body := f.call(t, f.admin, http.MethodPatch, "/admin/households/"+homeID+"/members/"+f.other.ID, map[string]any{"age_limit": 7}); code != http.StatusOK {
		t.Fatalf("set age limit %d %v", code, body)
	}
	if code, rest := f.call(t, f.other, http.MethodGet, "/content-restriction", nil); code != http.StatusOK || rest["max_age"].(float64) != 7 {
		t.Fatalf("restriction %d %v", code, rest)
	}

	if code, _ := f.call(t, f.admin, http.MethodDelete, "/admin/households/"+homeID+"/members/"+f.owner.ID, nil); code != http.StatusConflict {
		t.Fatalf("owner removed: %d", code)
	}
	if code, _ := f.call(t, f.admin, http.MethodPatch, "/admin/households/"+homeID, map[string]any{"owner_id": third.ID}); code != http.StatusBadRequest {
		t.Fatalf("ownership given to a non-member: %d", code)
	}

	if code, body := f.call(t, f.admin, http.MethodPost, "/admin/households/"+homeID+"/members/"+f.other.ID+"/move", map[string]any{"household_id": flatID}); code != http.StatusOK {
		t.Fatalf("move %d %v", code, body)
	}
	if n := f.countMemberships(t, f.other.ID); n != 1 {
		t.Fatalf("moved member has %d memberships", n)
	}
	var age int
	_ = f.api.DB.QueryRow(`SELECT age_limit FROM household_members WHERE household_id = ? AND user_id = ?`, flatID, f.other.ID).Scan(&age)
	if age != 7 {
		t.Fatalf("move lost the age limit: %d", age)
	}

	if code, body := f.call(t, f.admin, http.MethodPatch, "/admin/households/"+flatID, map[string]any{"owner_id": f.other.ID, "name": "Shared flat"}); code != http.StatusOK {
		t.Fatalf("transfer %d %v", code, body)
	}
	var owner, name string
	_ = f.api.DB.QueryRow(`SELECT owner_id, name FROM households WHERE id = ?`, flatID).Scan(&owner, &name)
	var prevRole string
	_ = f.api.DB.QueryRow(`SELECT role FROM household_members WHERE household_id = ? AND user_id = ?`, flatID, third.ID).Scan(&prevRole)
	if owner != f.other.ID || name != "Shared flat" || prevRole != "adult" {
		t.Fatalf("transfer stored owner=%s name=%s previous=%s", owner, name, prevRole)
	}

	if code, _ := f.call(t, f.admin, http.MethodDelete, "/admin/households/"+flatID, nil); code != http.StatusOK {
		t.Fatalf("delete %d", code)
	}
	if n := f.countMemberships(t, f.other.ID) + f.countMemberships(t, third.ID); n != 0 {
		t.Fatalf("deleted household left %d memberships", n)
	}
	if code, _ := f.call(t, f.admin, http.MethodGet, "/admin/households/"+flatID, nil); code != http.StatusNotFound {
		t.Fatalf("deleted household still readable: %d", code)
	}

	var audits int
	_ = f.api.DB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action LIKE 'household.admin.%'`).Scan(&audits)
	if audits < 5 {
		t.Fatalf("expected audited admin actions, got %d", audits)
	}
}

func TestHouseholdAdminRespectsPrivilegeCeiling(t *testing.T) {
	f := newFixture(t)
	_, home := f.call(t, f.owner, http.MethodPost, "/household", map[string]string{"name": "Home"})
	homeID := householdID(t, home)
	f.joinByInvite(t, f.owner, f.admin, 0)
	manager := &auth.Principal{Kind: auth.KindUser, UserID: f.other.ID, Username: f.other.Username, Permissions: []string{auth.PermUsersManage}}
	if code, _ := f.callAs(t, manager, http.MethodPatch, "/admin/households/"+homeID+"/members/"+f.admin.ID, map[string]any{"age_limit": 7}); code != http.StatusForbidden {
		t.Fatalf("user manager restricted an administrator: %d", code)
	}
	if code, _ := f.callAs(t, manager, http.MethodDelete, "/admin/households/"+homeID+"/members/"+f.admin.ID, nil); code != http.StatusForbidden {
		t.Fatalf("user manager removed an administrator: %d", code)
	}
	if code, _ := f.callAs(t, manager, http.MethodPatch, "/users/"+f.admin.ID, map[string]any{"content_age_limit": 7}); code != http.StatusForbidden {
		t.Fatalf("user manager set an administrator's content limit: %d", code)
	}
	if code, _ := f.callAs(t, manager, http.MethodGet, "/admin/households/"+homeID, nil); code != http.StatusOK {
		t.Fatalf("user manager could not view: %d", code)
	}
}

func TestUserContentAgeLimit(t *testing.T) {
	f := newFixture(t)
	if code, _ := f.call(t, f.admin, http.MethodPatch, "/users/"+f.other.ID, map[string]any{"content_age_limit": 40}); code != http.StatusBadRequest {
		t.Fatalf("out of range limit accepted: %d", code)
	}
	code, body := f.call(t, f.admin, http.MethodPatch, "/users/"+f.other.ID, map[string]any{"content_age_limit": 13})
	if code != http.StatusOK || body["content_age_limit"].(float64) != 13 {
		t.Fatalf("patch %d %v", code, body)
	}
	if rest, ok := body["content_restriction"].(map[string]any); !ok || rest["max_age"].(float64) != 13 {
		t.Fatalf("effective restriction missing: %v", body)
	}
	if code, _ := f.call(t, f.other, http.MethodPatch, "/users/"+f.other.ID, map[string]any{"content_age_limit": 0}); code != http.StatusForbidden {
		t.Fatalf("restricted user lifted their own limit: %d", code)
	}
	var n int
	_ = f.api.DB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'user.content_restriction'`).Scan(&n)
	if n != 1 {
		t.Fatalf("limit change not audited: %d", n)
	}
}
