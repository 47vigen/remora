package main

import (
	"fmt"
	"io"

	"github.com/47vigen/remora/internal/version"
)

// runVersion prints "remora <Version> (<Commit>)".
func runVersion(stdout io.Writer) int {
	if _, err := fmt.Fprintf(stdout, "remora %s (%s)\n", version.Version, version.Commit); err != nil {
		return 1
	}
	return 0
}
