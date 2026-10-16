package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadCooling(t *testing.T) {
	c, err := Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if want := (Cooling{Count429: 3, Window429: 60 * time.Second, Default: 5 * time.Minute, Poll: time.Second, SubscriptionDefault: 30 * time.Second}); c.Cooling != want {
		t.Errorf("defaults = %+v, want %+v", c.Cooling, want)
	}

	c, err = Load(envFrom(map[string]string{
		EnvCooling429Count: "5", EnvCooling429Window: "2m", EnvCoolingDefault: "30s", EnvCoolingPoll: "250ms",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if want := (Cooling{Count429: 5, Window429: 2 * time.Minute, Default: 30 * time.Second, Poll: 250 * time.Millisecond, SubscriptionDefault: 30 * time.Second}); c.Cooling != want {
		t.Errorf("overrides = %+v, want %+v", c.Cooling, want)
	}

	for env, v := range map[string]string{
		EnvCooling429Count: "0", EnvCooling429Window: "soon", EnvCoolingDefault: "-1s", EnvCoolingPoll: "0s",
	} {
		_, err := Load(envFrom(map[string]string{env: v}))
		if err == nil || !strings.Contains(err.Error(), env) {
			t.Errorf("%s=%q: err = %v, want one naming the variable", env, v, err)
		}
	}
}
