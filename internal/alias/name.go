package alias

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrInvalidName means an alias name does not match the allowed pattern.
var ErrInvalidName = errors.New("alias: invalid name")

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

// ValidName reports whether name may be used as an alias name: ^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$. A name may look
// like a provider model ("openai/fast"): the front door only ever sees EngineModel(name).
func ValidName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%w: %q must match %s", ErrInvalidName, name, namePattern)
	}
	return nil
}

// enginePrefix is never a provider name: remora reserves every "remora-" provider name for itself.
const enginePrefix = "remora-alias/"

// EngineModel is the model remora sends the front door for an alias. The front door reads "<provider>/<model>" as a
// direct provider model whenever the prefix names one of its providers, so "openai/fast" would never reach its
// routing rule; behind the prefix the whole alias stays one model name.
func EngineModel(name string) string { return enginePrefix + name }
