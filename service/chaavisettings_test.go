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

// A box upgraded from the adapter still has BW_* in its .env: the old password shows in
// Preferences, and saving rewrites the file with the new keys and none of the old ones.
func TestChaaviSettingsMigrateTheAdaptersKeys(t *testing.T) {
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
	if got.Env.Password != "oldpass" || got.Env.Email != "" {
		t.Fatalf("legacy password not shown: %+v", got.Env)
	}
	got.Env.Email = "me@example.com"
	if err := writeChaaviSettings(envPath, got); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "BW_") {
		t.Fatalf("old keys survived the save: %s", text)
	}
	for _, want := range []string{"CHAAVI_EMAIL=me@example.com", "CHAAVI_PASSWORD=oldpass", "KEEP_ME=yes"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
}
