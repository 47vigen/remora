package config

import "testing"

func TestEnvName(t *testing.T) {
	for _, c := range []struct{ provider, key, want string }{
		{"openrouter", "key-1", "REMORA_PROVIDER_OPENROUTER_KEY_1"},
		{"new", "main", "REMORA_PROVIDER_NEW_MAIN"},
		{"", "svc-a", "REMORA_KEY_SVC_A"},
		{"", "a.b c", "REMORA_KEY_A_B_C"},
	} {
		if got := EnvName(c.provider, c.key); got != c.want {
			t.Errorf("EnvName(%q, %q) = %q, want %q", c.provider, c.key, got, c.want)
		}
	}
}
