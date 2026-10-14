package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/47vigen/remora/internal/accounts"
	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/auth"
	"github.com/47vigen/remora/internal/config"
	"github.com/47vigen/remora/internal/reconcile"
	"github.com/47vigen/remora/internal/store"
)

// errNotImplemented answers 501 not_implemented: an operation whose handler or backing service is not wired yet.
var errNotImplemented = errors.New("not implemented")

// errForbidden answers 403 forbidden: the caller is authenticated, but not in a way that may do this.
var errForbidden = errors.New("forbidden")

// badRequest is a validation failure; its text is shown to the caller.
type badRequest string

func (e badRequest) Error() string { return string(e) }

// requestError wraps a failure to decode a request's parameters or body.
type requestError struct{ err error }

func (e requestError) Error() string { return e.err.Error() }
func (e requestError) Unwrap() error { return e.err }

// Error types, the same vocabulary as /v1.
const (
	typeInvalidRequest = "invalid_request_error"
	typeAuthentication = "authentication_error"
	typePermission     = "permission_error"
	typeNotFound       = "not_found_error"
	typeRateLimit      = "rate_limit_error"
	typeAPI            = "api_error"
)

// apiError is the status, type, code and message of an error answer.
type apiError struct {
	status         int
	typ, code, msg string
}

// toAPIError maps an error to its answer. Only remora's own validation texts reach the caller; anything else is
// an engine failure with a fixed message (the raw error is logged, never returned).
func toAPIError(err error) apiError {
	var (
		br badRequest
		re requestError
	)
	switch {
	case errors.Is(err, errForbidden):
		return apiError{http.StatusForbidden, typePermission, "forbidden", "Only a signed-in session may do this, not an API token."}
	case errors.Is(err, errNotImplemented):
		return apiError{http.StatusNotImplemented, typeAPI, "not_implemented", "This operation is not available yet."}
	case errors.As(err, &br):
		return apiError{http.StatusBadRequest, typeInvalidRequest, "invalid_request", string(br)}
	case errors.As(err, &re):
		return apiError{http.StatusBadRequest, typeInvalidRequest, "invalid_request", "Invalid request: " + re.Error()}
	case errors.Is(err, store.ErrNotFound):
		return apiError{http.StatusNotFound, typeNotFound, "not_found", "Not found."}
	case errors.Is(err, store.ErrDuplicate):
		return apiError{http.StatusConflict, typeInvalidRequest, "conflict", "Already exists."}
	case errors.Is(err, auth.ErrWeakPassword):
		return apiError{http.StatusBadRequest, typeInvalidRequest, "invalid_request", auth.ErrWeakPassword.Error()}
	case errors.Is(err, auth.ErrBadPassword):
		return apiError{http.StatusUnauthorized, typeAuthentication, "unauthorized", "Wrong password."}
	case errors.Is(err, auth.ErrUnauthenticated):
		return apiError{http.StatusUnauthorized, typeAuthentication, "unauthorized", "Sign in or send an API token."}
	case errors.Is(err, auth.ErrTooManyAttempts):
		return apiError{http.StatusTooManyRequests, typeRateLimit, "too_many_attempts", "Too many failed sign-in attempts; try again later."}
	}
	for _, target := range []error{accounts.ErrNotFound, accounts.ErrLoginNotFound} {
		if errors.Is(err, target) {
			return apiError{http.StatusNotFound, typeNotFound, "not_found", sentence(innermost(err, target))}
		}
	}
	switch {
	case errors.Is(err, accounts.ErrLoginExpired):
		return apiError{http.StatusGone, typeInvalidRequest, "login_expired", sentence(accounts.ErrLoginExpired)}
	case errors.Is(err, accounts.ErrCredentialMissing):
		return apiError{http.StatusConflict, typeInvalidRequest, "conflict", sentence(accounts.ErrCredentialMissing)}
	}
	for _, target := range []error{
		accounts.ErrUnknownProvider, accounts.ErrBadRedirect, accounts.ErrNotPasteFlow, accounts.ErrBadCredential,
		alias.ErrCycle, alias.ErrDepth, alias.ErrUnknownMember, alias.ErrEmpty, alias.ErrInvalidName, reconcile.ErrInvalid,
		config.ErrInvalid,
	} {
		if errors.Is(err, target) {
			return apiError{http.StatusBadRequest, typeInvalidRequest, "invalid_request", innermost(err, target).Error()}
		}
	}
	return apiError{http.StatusBadGateway, typeAPI, "engine_error", "An internal service call failed; see the server log."}
}

// sentence capitalises an accounts error's text for an answer ("login not found" → "Login not found.").
func sentence(err error) string {
	msg := err.Error()
	if msg == "" {
		return msg
	}
	msg = strings.ToUpper(msg[:1]) + msg[1:]
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}

// innermost returns the deepest error in err's chain that still wraps target with context of its own (for example
// "alias: cycle: a -> b -> a"), dropping the callers' prefixes such as "reconcile: change: store: write:".
func innermost(err, target error) error {
	e := err
	for {
		u := errors.Unwrap(e)
		if u == nil || u == target || !errors.Is(u, target) { //nolint:errorlint // identity: stop above the sentinel itself
			return e
		}
		e = u
	}
}

// writeError answers r with err in remora's error shape. It is the strict server's request and response error
// handler. An engine failure is logged with its raw text.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	e := toAPIError(err)
	if e.status >= http.StatusInternalServerError && e.status != http.StatusNotImplemented {
		s.log().ErrorContext(r.Context(), "management API call failed", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	auth.WriteError(w, e.status, e.typ, e.code, e.msg)
}

// requestErrorHandler answers a request the generated code could not decode.
func (s *Server) requestErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	s.writeError(w, r, requestError{err})
}
