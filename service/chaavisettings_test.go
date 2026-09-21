package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteChaaviDotEnvPreservesExtras(t *testing.T) {
	text := writeChaaviDotEnv(map[string]string{
		"BW_CLIENTID":     "id",
		"BW_CLIENTSECRET": "secret",
		"BW_PASSWORD":     "pass",
		"EXTRA_KEY":       "keep",
	})
	if !strings.Contains(text, "BW_CLIENTID=id") {
		t.Fatal(text)
	}
	if !strings.Contains(text, "EXTRA_KEY=keep") {
		t.Fatal("missing extra")
	}
}

func TestReadWriteChaaviSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("BW_CLIENTID=old\nKEEP_ME=yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed := chaaviSettings{}
	seed.Env.ClientID = "user.abc"
	seed.Env.ClientSecret = "secret"
	seed.Env.Password = "pass"
	if err := writeChaaviSettings(envPath, seed); err != nil {
		t.Fatal(err)
	}
	got, err := readChaaviSettings(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.Env.ClientID != "user.abc" || got.Env.ClientSecret != "secret" || got.Env.Password != "pass" {
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
	if got.Env.ClientID != "" || got.Env.ClientSecret != "" || got.Env.Password != "" {
		t.Fatalf("expected empty: %+v", got.Env)
	}
}
