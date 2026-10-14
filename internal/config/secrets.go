package config

import "strings"

// Secrets holds the values of a document's "env:NAME" references, by NAME.
type Secrets map[string]string

// EnvName names the environment variable that holds a secret: a provider key is
// "REMORA_PROVIDER_<PROVIDER>_<KEY>", a client key (provider "") is "REMORA_KEY_<NAME>". Letters are upper-cased and
// every other character that is not a letter or digit becomes "_".
func EnvName(provider, key string) string {
	if provider == "" {
		return "REMORA_KEY_" + envPart(key)
	}
	return "REMORA_PROVIDER_" + envPart(provider) + "_" + envPart(key)
}

func envPart(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
