// Package version holds the build version of remora, set at link time with
// -X github.com/47vigen/remora/internal/version.Version=... and .Commit=...
package version

// Version is the release version of this build. It is set with ldflags and is
// the one exception to the no-global-state rule.
var Version = "dev"

// Commit is the source commit of this build, set with ldflags.
var Commit = "none"
