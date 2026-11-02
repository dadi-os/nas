package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteChaaviDotEnvPreservesExtras(t *testing.T) {
	text := writeChaaviDotEnv(map[string]string{
		"CHAAVI_EMAIL":    "me@example.com",
		"CHAAVI_PASSWORD": "pass",
		"EXTRA_KEY":       "keep",
	})
	if !strings.Contains(text, "CHAAVI_EMAIL=me@example.com") {
		t.Fatal(text)
	}
	if !strings.Contains(text, "EXTRA_KEY=keep") {
		t.Fatal("missing extra")
	}
}

func TestReadWriteChaaviSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("CHAAVI_EMAIL=old@example.com\nKEEP_ME=yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed := chaaviSettings{}
	seed.Env.Email = "me@example.com"
	seed.Env.Password = "pass"
	if err := writeChaaviSettings(envPath, seed); err != nil {
		t.Fatal(err)
	}
	got, err := readChaaviSettings(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.Env.Email != "me@example.com" || got.Env.Password != "pass" {
		t.Fatalf("env overlay: %+v", got.Env)
	}
	body, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "KEEP_ME=yes") {
		t.Fatalf("lost extra key: %s", body)
	}
}

func TestReadWriteChaaviSettingsAllowsEmpty(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := writeChaaviSettings(envPath, chaaviSettings{}); err != nil {
		t.Fatal(err)
	}
	got, err := readChaaviSettings(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.Env.Email != "" || got.Env.Password != "" {
		t.Fatalf("expected empty: %+v", got.Env)
	}
}

// An .env that still has the adapter's BW_* keys is not read as the vault account: Preferences
// shows it empty, and a save writes the CHAAVI_* keys and keeps every other key.
func TestChaaviSettingsIgnoreTheAdaptersKeys(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	old := "BW_CLIENTID=id\nBW_CLIENTSECRET=secret\nBW_PASSWORD=oldpass\nKEEP_ME=yes\n"
	if err := os.WriteFile(envPath, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readChaaviSettings(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.Env.Password != "" || got.Env.Email != "" {
		t.Fatalf("old keys were read as the vault account: %+v", got.Env)
	}
	got.Env.Email = "me@example.com"
	got.Env.Password = "newpass"
	if err := writeChaaviSettings(envPath, got); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"CHAAVI_EMAIL=me@example.com", "CHAAVI_PASSWORD=newpass", "KEEP_ME=yes"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
}
