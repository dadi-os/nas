//go:build linux

package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAsFSUserChecksAndOwnsAsTheUser(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("switching filesystem ids needs root")
	}
	const nobody = 65534
	dir, err := os.MkdirTemp("", "fsuser-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(dir, "open")
	closed := filepath.Join(dir, "closed")
	if err := os.Mkdir(open, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(closed, 0o700); err != nil {
		t.Fatal(err)
	}

	var made, denied error
	err = asFSUser(nobody, nobody, func() {
		made = os.MkdirAll(filepath.Join(open, "a", "b"), 0o755)
		denied = os.WriteFile(filepath.Join(closed, "x"), []byte("x"), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if made != nil {
		t.Fatalf("create in open dir: %v", made)
	}
	for _, p := range []string{filepath.Join(open, "a"), filepath.Join(open, "a", "b")} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if uid := fi.Sys().(*syscall.Stat_t).Uid; uid != nobody {
			t.Fatalf("%s owned by %d, want %d", p, uid, nobody)
		}
	}
	if !errors.Is(denied, fs.ErrPermission) {
		t.Fatalf("write into root-only dir: got %v, want permission denied", denied)
	}
	if err := os.WriteFile(filepath.Join(closed, "root"), []byte("x"), 0o644); err != nil {
		t.Fatalf("caller lost root after asFSUser: %v", err)
	}
}
