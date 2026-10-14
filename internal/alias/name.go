package alias

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrInvalidName means an alias name does not match the allowed pattern.
var ErrInvalidName = errors.New("alias: invalid name")

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

// ValidName reports whether name may be used as an alias name: ^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$. The rule that
// the part before a "/" must not be a provider name needs the provider list and is checked by the caller.
func ValidName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%w: %q must match %s", ErrInvalidName, name, namePattern)
	}
	return nil
}
