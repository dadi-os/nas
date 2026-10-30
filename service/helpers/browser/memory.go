package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// browserReserve is the memory one more browser is expected to need: Chromium with its
// renderers and an Xvfb on a heavy page.
const browserReserve = 800 << 20

// cgroupMemory is a cgroup's memory limit and use, in bytes. Unlimited means memory.max
// holds "max".
type cgroupMemory struct {
	Max       int64
	Current   int64
	Unlimited bool
}

// readCgroup reads memory.max and memory.current of the cgroup at dir.
func readCgroup(dir string) (cgroupMemory, error) {
	var out cgroupMemory
	maxRaw, err := os.ReadFile(filepath.Join(dir, "memory.max"))
	if err != nil {
		return out, fmt.Errorf("read cgroup memory limit: %w", err)
	}
	curRaw, err := os.ReadFile(filepath.Join(dir, "memory.current"))
	if err != nil {
		return out, fmt.Errorf("read cgroup memory use: %w", err)
	}
	if v := strings.TrimSpace(string(maxRaw)); v == "max" {
		out.Unlimited = true
	} else if out.Max, err = strconv.ParseInt(v, 10, 64); err != nil {
		return out, fmt.Errorf("parse %s/memory.max: %w", dir, err)
	}
	if out.Current, err = strconv.ParseInt(strings.TrimSpace(string(curRaw)), 10, 64); err != nil {
		return out, fmt.Errorf("parse %s/memory.current: %w", dir, err)
	}
	return out, nil
}

// hasRoom reports whether the cgroup can take need more bytes under its limit.
func (m cgroupMemory) hasRoom(need int64) bool {
	return m.Unlimited || m.Current+need <= m.Max
}
