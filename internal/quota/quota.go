// Package quota reads how much of each subscription account's quota is left. It sends each provider's private usage
// request upstream through the OAuth engine as the account's own credential, keeps one snapshot per account in the
// store, and polls accounts one at a time at a bounded upstream cost.
package quota

import "strings"

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
	MsgSuspended   = "the provider suspended this account"
	MsgRateLimited = "the provider is limiting quota checks; retrying later"
	MsgUnavailable = "the provider is unavailable; retrying later"
	MsgRefused     = "the provider refused the request"
	MsgUnreadable  = "the provider's answer was not understood"
	MsgFailed      = "the quota check failed"
)

// Suspended reports whether a snapshot error says the provider suspended the account; the provider's own words follow.
func Suspended(msg string) bool { return strings.HasPrefix(msg, MsgSuspended) }

// NeedsOwner reports whether a snapshot error waits on the owner: a new sign-in or an appeal to the provider.
func NeedsOwner(msg string) bool { return msg == MsgSignIn || Suspended(msg) }
