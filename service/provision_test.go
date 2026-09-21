package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNodeNameTaken(t *testing.T) {
	taken := []string{"os", "ankur-phone"}
	if !nodeNameTaken("OS", taken) {
		t.Fatal("expected case-insensitive match")
	}
	if !nodeNameTaken(" ankur-phone ", taken) {
		t.Fatal("expected trim match")
	}
	if nodeNameTaken("laptop", taken) {
		t.Fatal("laptop should be free")
	}
}

func TestTakenNodeNamesIncludesPending(t *testing.T) {
	now := time.Date(2026, 9, 14, 21, 0, 0, 0, time.UTC)
	clients := []meshClient{{NodeName: "os"}}
	pending := map[string]time.Time{
		"phone": now.Add(time.Hour),
		"stale": now.Add(-time.Minute),
		"os":    now.Add(time.Hour),
	}
	got := takenNodeNames(clients, pending, now)
	want := map[string]bool{"os": true, "phone": true}
	if len(got) != 2 {
		t.Fatalf("len %d: %v", len(got), got)
	}
	for _, n := range got {
		if !want[n] {
			t.Fatalf("unexpected %q in %v", n, got)
		}
	}
}

func TestPendingNodesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 14, 21, 0, 0, 0, time.UTC)
	first, err := loadPendingNodes(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 0 {
		t.Fatalf("empty dir: %+v", first)
	}

	err = savePendingNodes(dir, map[string]time.Time{
		"phone": now.Add(time.Hour),
		"old":   now.Add(-time.Second),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, pendingNodesFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "old") {
		t.Fatalf("save should drop expired names: %s", raw)
	}
	loaded, err := loadPendingNodes(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded["phone"]; !ok {
		t.Fatalf("missing phone: %+v", loaded)
	}
	if _, ok := loaded["old"]; ok {
		t.Fatal("expired name should be dropped on load")
	}

	if err := os.WriteFile(filepath.Join(dir, pendingNodesFile), nil, 0600); err != nil {
		t.Fatal(err)
	}
	empty, err := loadPendingNodes(dir, now)
	if err != nil {
		t.Fatalf("empty file: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty file should be empty map: %+v", empty)
	}

	if err := os.WriteFile(filepath.Join(dir, pendingNodesFile), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPendingNodes(dir, now); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestPruneJoinedPending(t *testing.T) {
	pending := map[string]time.Time{
		"mac":   time.Now().Add(time.Hour),
		"phone": time.Now().Add(time.Hour),
	}
	pruneJoinedPending(pending, []meshClient{{NodeName: "Mac"}})
	if _, ok := pending["mac"]; ok {
		t.Fatal("joined name should leave pending")
	}
	if _, ok := pending["phone"]; !ok {
		t.Fatal("unjoined name should stay pending")
	}
}

func TestWithPendingClients(t *testing.T) {
	now := time.Date(2026, 9, 14, 22, 0, 0, 0, time.UTC)
	clients := []meshClient{{NodeName: "os", Online: true, IPAddresses: []string{}}}
	pending := map[string]time.Time{
		"zebra": now.Add(time.Hour),
		"mac":   now.Add(time.Hour),
		"os":    now.Add(time.Hour),
		"stale": now.Add(-time.Minute),
	}
	got := withPendingClients(clients, pending, now)
	if len(got) != 3 {
		t.Fatalf("len %d: %+v", len(got), got)
	}
	if got[0].NodeName != "os" || got[0].Pending {
		t.Fatalf("mesh row first: %+v", got[0])
	}
	if got[1].NodeName != "mac" || !got[1].Pending {
		t.Fatalf("pending sorted: %+v", got[1])
	}
	if got[2].NodeName != "zebra" || !got[2].Pending {
		t.Fatalf("pending sorted: %+v", got[2])
	}
}

func TestWithPendingClientsEmpty(t *testing.T) {
	now := time.Date(2026, 9, 14, 22, 0, 0, 0, time.UTC)
	got := withPendingClients(nil, map[string]time.Time{}, now)
	if got == nil {
		t.Fatal("empty list must be non-nil JSON array")
	}
	if len(got) != 0 {
		t.Fatalf("len %d", len(got))
	}
}
