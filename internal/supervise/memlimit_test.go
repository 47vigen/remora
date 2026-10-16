package supervise

import (
	"errors"
	"slices"
	"testing"
)

func TestMemoryLimitShares(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		err  error
		want int64
	}{
		{"limit", "4294967296\n", nil, 4294967296},
		{"no limit", "max\n", nil, 0},
		{"unreadable", "", errors.New("no cgroup v2"), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := MemoryLimit(func(path string) ([]byte, error) {
				if path != "/sys/fs/cgroup/memory.max" {
					t.Errorf("read %q", path)
				}
				return []byte(tc.data), tc.err
			})
			if got != tc.want {
				t.Errorf("MemoryLimit = %d, want %d", got, tc.want)
			}
		})
	}
	if got := GoMemLimitEnv(4294967296, 0.5); !slices.Equal(got, []string{"GOMEMLIMIT=2147483648"}) {
		t.Errorf("GoMemLimitEnv(4 GiB, 0.5) = %q", got)
	}
	if got := GoMemLimitEnv(0, 0.5); got != nil {
		t.Errorf("GoMemLimitEnv(0, 0.5) = %q, want nil", got)
	}
}
