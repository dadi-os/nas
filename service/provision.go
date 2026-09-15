package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const pendingNodesFile = "pending-nodes.json"
const pendingNodeTTL = time.Hour

var provisionMu sync.Mutex

// rollbackPendingNode removes a reserved name after a failed mint and persists the map.
func rollbackPendingNode(stateDir string, pending map[string]time.Time, key string) {
	delete(pending, key)
	if err := savePendingNodes(stateDir, pending, time.Now()); err != nil {
		slog.Error("rollback pending node", "err", err)
	}
}

// nodeNameTaken reports whether name collides with an existing or pending node.
func nodeNameTaken(name string, taken []string) bool {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return false
	}
	for _, n := range taken {
		if strings.ToLower(strings.TrimSpace(n)) == want {
			return true
		}
	}
	return false
}

// takenNodeNames is registered mesh names plus unexpired pending setup names.
func takenNodeNames(clients []meshClient, pending map[string]time.Time, now time.Time) []string {
	seen := map[string]struct{}{}
	var names []string
	add := func(raw string) {
		n := strings.ToLower(strings.TrimSpace(raw))
		if n == "" {
			return
		}
		if _, ok := seen[n]; ok {
			return
		}
		seen[n] = struct{}{}
		names = append(names, n)
	}
	for _, c := range clients {
		add(c.NodeName)
	}
	for name, until := range pending {
		if !until.After(now) {
			continue
		}
		add(name)
	}
	return names
}

// pruneJoinedPending drops reservations for names that already exist on the mesh.
func pruneJoinedPending(pending map[string]time.Time, clients []meshClient) {
	have := map[string]struct{}{}
	for _, c := range clients {
		n := strings.ToLower(strings.TrimSpace(c.NodeName))
		if n != "" {
			have[n] = struct{}{}
		}
	}
	for name := range pending {
		if _, ok := have[name]; ok {
			delete(pending, name)
		}
	}
}

// withPendingClients appends unexpired pending names that are not already mesh nodes.
func withPendingClients(clients []meshClient, pending map[string]time.Time, now time.Time) []meshClient {
	have := map[string]struct{}{}
	for _, c := range clients {
		have[strings.ToLower(strings.TrimSpace(c.NodeName))] = struct{}{}
	}
	var extra []string
	for name, until := range pending {
		if name == "" || !until.After(now) {
			continue
		}
		if _, ok := have[name]; ok {
			continue
		}
		extra = append(extra, name)
	}
	sort.Strings(extra)
	out := make([]meshClient, 0, len(clients)+len(extra))
	out = append(out, clients...)
	for _, name := range extra {
		out = append(out, meshClient{
			NodeName:    name,
			Online:      false,
			Pending:     true,
			IPAddresses: []string{},
		})
	}
	return out
}

// loadPendingNodes reads pending-nodes.json, dropping expired reservations.
func loadPendingNodes(dir string, now time.Time) (map[string]time.Time, error) {
	path := filepath.Join(dir, pendingNodesFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]time.Time{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pending nodes: %w", err)
	}
	var encoded map[string]string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("parse pending nodes: %w", err)
	}
	out := map[string]time.Time{}
	for name, stamp := range encoded {
		until, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			return nil, fmt.Errorf("parse pending node expiry: %w", err)
		}
		if until.After(now) {
			out[strings.ToLower(strings.TrimSpace(name))] = until
		}
	}
	return out, nil
}

// savePendingNodes writes unexpired pending names as RFC3339 timestamps (mode 0600).
func savePendingNodes(dir string, pending map[string]time.Time, now time.Time) error {
	encoded := map[string]string{}
	for name, until := range pending {
		if name == "" || !until.After(now) {
			continue
		}
		encoded[name] = until.UTC().Format(time.RFC3339)
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return fmt.Errorf("encode pending nodes: %w", err)
	}
	path := filepath.Join(dir, pendingNodesFile)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		return fmt.Errorf("write pending nodes: %w", err)
	}
	return nil
}
