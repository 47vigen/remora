// Package quota reads how much of each subscription account's quota is left. It sends each provider's private usage
// request upstream through the OAuth engine as the account's own credential, keeps one snapshot per account in the
// store, and polls accounts one at a time at a bounded upstream cost.
package quota

// Supported reports whether remora can read the quota of provider's accounts.
func Supported(provider string) bool {
	switch provider {
	case "antigravity", "codex", "claude":
		return true
	}
	return false
}

// User-facing snapshot errors.
const (
	MsgSignIn      = "sign in again"
	MsgRateLimited = "the provider is limiting quota checks; retrying later"
	MsgUnavailable = "the provider is unavailable; retrying later"
	MsgRefused     = "the provider refused the request"
	MsgUnreadable  = "the provider's answer was not understood"
	MsgFailed      = "the quota check failed"
)
