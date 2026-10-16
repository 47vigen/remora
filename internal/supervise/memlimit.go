package supervise

import (
	"strconv"
	"strings"
)

// MemoryShares split the container's memory limit between the processes: each Go runtime gets its share as a soft
// limit (GOMEMLIMIT), so it collects garbage harder before the container as a whole is killed. The rest is headroom
// for the speech adapter, SQLite caches and what the runtimes keep outside their heaps.
var MemoryShares = struct{ Router, Accounts, Self float64 }{0.50, 0.15, 0.10}

// MemoryLimit returns the cgroup v2 memory limit in bytes, read through read, or 0 when there is none: the file is
// missing or unreadable (no cgroup v2, as on macOS), or holds "max".
func MemoryLimit(read func(string) ([]byte, error)) int64 {
	data, err := read("/sys/fs/cgroup/memory.max")
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// GoMemLimitEnv returns the GOMEMLIMIT entry for a child that gets share of limit, or nil when there is no limit.
func GoMemLimitEnv(limit int64, share float64) []string {
	if limit <= 0 {
		return nil
	}
	return []string{"GOMEMLIMIT=" + strconv.FormatInt(int64(float64(limit)*share), 10)}
}
