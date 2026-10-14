package omniroute

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
)

func decodeCredential(t *testing.T, c Credential) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(c.JSON, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAntigravityFile(t *testing.T) {
	c := Connection{
		ID: "c4", Provider: "antigravity", AuthType: "oauth", Email: "a@example.com", ProjectID: "proj-test-1",
		AccessToken: "ag-access", RefreshToken: "ag-refresh", ExpiresAt: "2026-10-14T13:00:00.000Z", ExpiresIn: 3600, Active: true,
	}
	cred, err := antigravityFile(c, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Provider != "antigravity" || cred.FileName != "antigravity-a@example.com.json" {
		t.Fatalf("got %+v", cred)
	}
	m := decodeCredential(t, cred)
	keys := slices.Sorted(maps.Keys(m))
	if want := []string{"access_token", "email", "expired", "expires_in", "project_id", "refresh_token", "timestamp", "type"}; !slices.Equal(keys, want) {
		t.Fatalf("keys %q, want %q", keys, want)
	}
	if m["type"] != "antigravity" || m["access_token"] != "ag-access" || m["refresh_token"] != "ag-refresh" ||
		m["email"] != "a@example.com" || m["project_id"] != "proj-test-1" || m["expires_in"] != float64(3600) ||
		m["expired"] != "2026-10-14T13:00:00Z" || m["timestamp"] != float64(fixtureNow.UnixMilli()) {
		t.Fatalf("got %v", m)
	}

	c.RefreshToken = ""
	if _, err := antigravityFile(c, fixtureNow); err == nil {
		t.Fatal("no refresh token: want an error")
	}
}

func TestCodexFile(t *testing.T) {
	c := Connection{
		ID: "c5", Provider: "codex", AuthType: "oauth", Email: "c@example.com",
		AccessToken: "cx-access", RefreshToken: "cx-refresh", IDToken: fixtureIDToken, ExpiresAt: "2026-10-20T12:00:00.000Z", ExpiresIn: 864000,
	}
	cred, err := codexFile(c)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Provider != "codex" || cred.FileName != "codex-c@example.com.json" {
		t.Fatalf("got %+v", cred)
	}
	m := decodeCredential(t, cred)
	keys := slices.Sorted(maps.Keys(m))
	if want := []string{"access_token", "account_id", "email", "expired", "id_token", "last_refresh", "plan_type", "refresh_token", "type"}; !slices.Equal(keys, want) {
		t.Fatalf("keys %q, want %q", keys, want)
	}
	if m["type"] != "codex" || m["account_id"] != "acc-1" || m["plan_type"] != "plus" || m["id_token"] != fixtureIDToken ||
		m["refresh_token"] != "cx-refresh" || m["email"] != "c@example.com" || m["expired"] != "2026-10-20T12:00:00Z" ||
		m["last_refresh"] != "2026-10-10T12:00:00Z" {
		t.Fatalf("got %v", m)
	}

	// The email falls back to the id_token's claim; a malformed id_token is an error.
	c.Email = ""
	if cred, err := codexFile(c); err != nil || cred.FileName != "codex-c@example.com.json" {
		t.Fatalf("got %+v, %v", cred, err)
	}
	c.IDToken = "not-a-jwt"
	if _, err := codexFile(c); err == nil {
		t.Fatal("malformed id_token: want an error")
	}
}

func TestConvertCredentials(t *testing.T) {
	r := convertFixture(t)
	if len(r.Credentials) != 2 || r.Credentials[0].Provider != "antigravity" || r.Credentials[1].Provider != "codex" {
		t.Fatalf("credentials: %+v", r.Credentials)
	}
	if m := decodeCredential(t, r.Credentials[0]); m["access_token"] != fxAGAccess || m["refresh_token"] != fxAGRefresh {
		t.Fatalf("antigravity: %v", m)
	}
}
