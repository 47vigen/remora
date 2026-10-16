package quota

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

// Engine is what quota needs from the OAuth engine; *cliproxyapi.Client implements it.
type Engine interface {
	Credentials(ctx context.Context) ([]cliproxyapi.Credential, error)
	APICall(ctx context.Context, in cliproxyapi.APICallRequest) (cliproxyapi.APICallResponse, error)
	RefreshCredential(ctx context.Context, name string) error
}

// The engine's own user agents and Antigravity version source; enginepin_test.go keeps them equal to the engine's.
const (
	codexUserAgent          = "codex-tui/0.154.0 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.154.0)"
	claudeUserAgent         = "claude-cli/2.1.280 (external, cli)"
	antigravityManifestURL  = "https://antigravity-hub-auto-updater-974169037036.us-central1.run.app/manifest/latest-arm64-mac.yml"
	antigravityVersionFloor = "2.23.0"
	antigravityPlatform     = "darwin/arm64"
	antigravityVersionTTL   = 3 * time.Hour
	planTTL                 = 24 * time.Hour
)

const (
	agDailyBase = "https://daily-cloudcode-pa.googleapis.com"
	agProdBase  = "https://cloudcode-pa.googleapis.com"
	urlCodexUse = "https://chatgpt.com/backend-api/wham/usage"
	urlClaudeUs = "https://api.anthropic.com/api/oauth/usage"
)

var (
	// ErrUnsupported is returned for a provider whose quota remora cannot read.
	ErrUnsupported = errors.New("quota is not available for this provider")
	errSignIn      = errors.New(MsgSignIn)
	errUnreadable  = errors.New(MsgUnreadable)
)

// upstreamError is a non-2xx provider answer.
type upstreamError struct{ Status int }

func (e *upstreamError) Error() string { return "quota: provider answered " + strconv.Itoa(e.Status) }

type fetcher struct {
	eng   Engine
	base  string // "" = the providers' hosts
	ver   *versionCache
	mu    sync.Mutex
	plans map[int64]time.Time // last Antigravity plan read per account
}

// upstream puts raw onto base: base's scheme and host, base's path before raw's path. An empty base returns raw.
func upstream(base, raw string) string {
	if base == "" {
		return raw
	}
	b, err1 := url.Parse(base)
	u, err2 := url.Parse(raw)
	if err1 != nil || err2 != nil {
		return raw
	}
	u.Scheme, u.Host = b.Scheme, b.Host
	u.Path = strings.TrimRight(b.Path, "/") + u.Path
	return u.String()
}

// call sends one request as credential c and returns the body of a 2xx answer.
func (f *fetcher) call(ctx context.Context, c cliproxyapi.Credential, method, raw, data string, header map[string]string) ([]byte, error) {
	header["Authorization"] = "Bearer $TOKEN$"
	resp, err := f.eng.APICall(ctx, cliproxyapi.APICallRequest{
		AuthIndex: c.AuthIndex, Method: method, URL: upstream(f.base, raw), Header: header, Data: data,
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &upstreamError{Status: resp.StatusCode}
	}
	return []byte(resp.Body), nil
}

func unreadable(err error) error { return fmt.Errorf("%w: %w", errUnreadable, err) }

// fetch returns Plan, Windows and LimitReached of account a; the worker stamps times and errors.
func (f *fetcher) fetch(ctx context.Context, a store.Account, c cliproxyapi.Credential, prev store.AccountQuota) (store.AccountQuota, error) {
	switch a.Provider {
	case "antigravity":
		return f.antigravity(ctx, a, c, prev)
	case "codex":
		h := map[string]string{"User-Agent": codexUserAgent}
		if c.IDToken != nil && c.IDToken.ChatGPTAccountID != "" {
			h["chatgpt-account-id"] = c.IDToken.ChatGPTAccountID
		}
		body, err := f.call(ctx, c, http.MethodGet, urlCodexUse, "", h)
		if err != nil {
			return store.AccountQuota{}, err
		}
		q, err := parseCodexUsage(body, time.Now())
		if err != nil {
			return store.AccountQuota{}, unreadable(err)
		}
		if q.Plan == "" && c.IDToken != nil {
			q.Plan = c.IDToken.PlanType
		}
		return q, nil
	case "claude":
		body, err := f.call(ctx, c, http.MethodGet, urlClaudeUs, "",
			map[string]string{"anthropic-beta": "oauth-2025-04-20", "User-Agent": claudeUserAgent})
		if err != nil {
			return store.AccountQuota{}, err
		}
		q, err := parseClaudeUsage(body)
		if err != nil {
			return store.AccountQuota{}, unreadable(err)
		}
		return q, nil
	}
	return store.AccountQuota{}, ErrUnsupported
}

func (f *fetcher) antigravity(ctx context.Context, a store.Account, c cliproxyapi.Credential, prev store.AccountQuota) (store.AccountQuota, error) {
	if c.ProjectID == "" {
		return store.AccountQuota{}, errSignIn
	}
	headers := func() map[string]string {
		return map[string]string{"Content-Type": "application/json", "User-Agent": f.ver.userAgent(ctx)}
	}
	data := `{"project":` + strconv.Quote(c.ProjectID) + `}`
	body, err := f.call(ctx, c, http.MethodPost, agDailyBase+"/v1internal:retrieveUserQuotaSummary", data, headers())
	var ue *upstreamError
	var ee *cliproxyapi.Error
	if (errors.As(err, &ue) && (ue.Status == http.StatusNotFound || ue.Status >= 500)) ||
		(errors.As(err, &ee) && ee.StatusCode == http.StatusBadGateway) {
		body, err = f.call(ctx, c, http.MethodPost, agProdBase+"/v1internal:retrieveUserQuotaSummary", data, headers())
	}
	if err != nil {
		return store.AccountQuota{}, err
	}
	q, err := parseAntigravitySummary(body)
	if err != nil {
		return store.AccountQuota{}, unreadable(err)
	}
	q.Plan = prev.Plan
	f.mu.Lock()
	due := time.Since(f.plans[a.ID]) >= planTTL
	f.mu.Unlock()
	// shortcut: plan read times are in memory, so a restart reads each plan once more; persist them if restarts become frequent
	if due {
		plan, err := f.call(ctx, c, http.MethodPost, agProdBase+"/v1internal:loadCodeAssist",
			`{"metadata":{"ideType":"ANTIGRAVITY"}}`, headers())
		if err == nil {
			f.mu.Lock()
			f.plans[a.ID] = time.Now()
			f.mu.Unlock()
			if p := parseAntigravityPlan(plan); p != "" {
				q.Plan = p
			}
		}
	}
	return q, nil
}
