package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureChaaviTLSIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := stateConfig{dir: dir, runtime: "compose"}
	if err := s.ensureChaaviTLS(); err != nil {
		t.Fatal(err)
	}
	ca1, err := os.ReadFile(filepath.Join(dir, "caddy", "tls", "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	leaf1, err := os.ReadFile(filepath.Join(dir, "caddy", "tls", "chaavi.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ca1), "BEGIN CERTIFICATE") {
		t.Fatalf("ca.crt not PEM: %q", ca1[:32])
	}
	if err := s.ensureChaaviTLS(); err != nil {
		t.Fatal(err)
	}
	ca2, err := os.ReadFile(filepath.Join(dir, "caddy", "tls", "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(ca1) != string(ca2) {
		t.Fatal("ensureChaaviTLS regenerated CA")
	}
	leaf2, err := os.ReadFile(filepath.Join(dir, "caddy", "tls", "chaavi.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(leaf1) != string(leaf2) {
		t.Fatal("ensureChaaviTLS regenerated leaf")
	}
	pem, err := s.readChaaviCAPem()
	if err != nil {
		t.Fatal(err)
	}
	if pem != string(ca1) {
		t.Fatal("readChaaviCAPem mismatch")
	}
}

func TestEnsureChaaviTLSReusesCAWhenLeafMissing(t *testing.T) {
	dir := t.TempDir()
	s := stateConfig{dir: dir, runtime: "compose"}
	if err := s.ensureChaaviTLS(); err != nil {
		t.Fatal(err)
	}
	ca1, err := os.ReadFile(filepath.Join(dir, "caddy", "tls", "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	tlsDir := filepath.Join(dir, "caddy", "tls")
	if err := os.Remove(filepath.Join(tlsDir, "chaavi.crt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(tlsDir, "chaavi.key")); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureChaaviTLS(); err != nil {
		t.Fatal(err)
	}
	ca2, err := os.ReadFile(filepath.Join(tlsDir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(ca1) != string(ca2) {
		t.Fatal("ensureChaaviTLS regenerated CA when only leaf was missing")
	}
	if _, err := os.Stat(filepath.Join(tlsDir, "chaavi.crt")); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureChaaviTLSRejectsIncompleteCA(t *testing.T) {
	dir := t.TempDir()
	s := stateConfig{dir: dir, runtime: "compose"}
	tlsDir := filepath.Join(dir, "caddy", "tls")
	if err := os.MkdirAll(tlsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tlsDir, "ca.crt"), []byte("not-a-cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := s.ensureChaaviTLS()
	if err == nil || !strings.Contains(err.Error(), "incomplete mesh CA") {
		t.Fatalf("got %v", err)
	}
}
