package omniroute

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Credential is an OAuth credential file in the shape the accounts engine stores, ready for POST /api/accounts/import.
type Credential struct {
	Provider, FileName string
	JSON               []byte
}

// antigravityCredential mirrors the metadata the accounts engine writes for an Antigravity login
// (sdk/auth/antigravity.go, BuildAntigravityAuth).
type antigravityCredential struct {
	Type         string `json:"type"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Timestamp    int64  `json:"timestamp"` // unix ms of when the access token was issued
	Expired      string `json:"expired"`   // RFC 3339
	Email        string `json:"email,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
}

// antigravityFile converts an Antigravity connection. The issue time is the expiry less expires_in when both are
// known, and now otherwise.
func antigravityFile(c Connection, now time.Time) (Credential, error) {
	if c.RefreshToken == "" {
		return Credential{}, errors.New("no refresh token")
	}
	issued := now
	exp, ok := parseTime(c.ExpiresAt)
	switch {
	case ok && c.ExpiresIn > 0:
		issued = exp.Add(-time.Duration(c.ExpiresIn) * time.Second)
	case !ok:
		exp = now.Add(time.Duration(c.ExpiresIn) * time.Second)
	}
	email := strings.TrimSpace(c.Email)
	b, err := json.Marshal(antigravityCredential{ //nolint:gosec // G117: the credential file carries the tokens on purpose.
		Type: "antigravity", AccessToken: c.AccessToken, RefreshToken: c.RefreshToken, ExpiresIn: c.ExpiresIn,
		Timestamp: issued.UnixMilli(), Expired: exp.UTC().Format(time.RFC3339), Email: email,
		ProjectID: strings.TrimSpace(c.ProjectID),
	})
	if err != nil {
		return Credential{}, err
	}
	name := "antigravity.json" // the engine's own name for an account without an email
	if email != "" {
		name = fmt.Sprintf("antigravity-%s.json", email)
	}
	return Credential{Provider: "antigravity", FileName: name, JSON: b}, nil
}

// codexCredential mirrors the engine's Codex token storage (internal/auth/codex/token.go, CodexTokenStorage).
type codexCredential struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
	LastRefresh  string `json:"last_refresh"`
	Email        string `json:"email"`
	Type         string `json:"type"`
	Expired      string `json:"expired"`
	PlanType     string `json:"plan_type,omitempty"`
}

// codexClaims are the id_token claims the engine reads (internal/auth/codex/jwt_parser.go).
type codexClaims struct {
	Email string `json:"email"`
	Auth  struct {
		AccountID string `json:"chatgpt_account_id"`
		PlanType  string `json:"chatgpt_plan_type"`
	} `json:"https://api.openai.com/auth"`
}

// codexFile converts a Codex connection. account_id and plan_type come from the id_token's payload, which is
// decoded, not verified: it was issued to OmniRoute and remora only carries it over.
func codexFile(c Connection) (Credential, error) {
	if c.RefreshToken == "" {
		return Credential{}, errors.New("no refresh token")
	}
	claims, err := jwtClaims(c.IDToken)
	if err != nil {
		return Credential{}, fmt.Errorf("id_token: %w", err)
	}
	email := strings.TrimSpace(c.Email)
	if email == "" {
		email = claims.Email
	}
	cred := codexCredential{
		IDToken: c.IDToken, AccessToken: c.AccessToken, RefreshToken: c.RefreshToken, AccountID: claims.Auth.AccountID,
		Email: email, Type: "codex", PlanType: claims.Auth.PlanType,
	}
	if exp, ok := parseTime(c.ExpiresAt); ok {
		cred.Expired = exp.UTC().Format(time.RFC3339)
		if c.ExpiresIn > 0 {
			cred.LastRefresh = exp.Add(-time.Duration(c.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
		}
	}
	b, err := json.Marshal(cred) //nolint:gosec // G117: the credential file carries the tokens on purpose.
	if err != nil {
		return Credential{}, err
	}
	name := "codex.json"
	if email != "" {
		name = fmt.Sprintf("codex-%s.json", email)
	}
	return Credential{Provider: "codex", FileName: name, JSON: b}, nil
}

func jwtClaims(token string) (codexClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return codexClaims{}, errors.New("not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return codexClaims{}, fmt.Errorf("payload: %w", err)
	}
	var c codexClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return codexClaims{}, fmt.Errorf("payload: %w", err)
	}
	return c, nil
}
