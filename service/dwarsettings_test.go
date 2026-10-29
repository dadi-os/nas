package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDotEnv(t *testing.T) {
	got := parseDotEnv("# hi\nANTHROPIC_API_KEY=abc\nGEMINI_API_KEY=\"x y\"\n\nOPENAI_API_KEY=\n")
	if got["ANTHROPIC_API_KEY"] != "abc" {
		t.Fatalf("anthropic: %q", got["ANTHROPIC_API_KEY"])
	}
	if got["GEMINI_API_KEY"] != "x y" {
		t.Fatalf("gemini quotes: %q", got["GEMINI_API_KEY"])
	}
	if got["OPENAI_API_KEY"] != "" {
		t.Fatalf("empty openai: %q", got["OPENAI_API_KEY"])
	}
}

func TestWriteDotEnvPreservesExtras(t *testing.T) {
	text := writeDotEnv(map[string]string{
		"ANTHROPIC_API_KEY": "a",
		"GEMINI_API_KEY":    "b",
		"OPENAI_API_KEY":    "c",
		"DEEPGRAM_API_KEY":  "d",
		"EXTRA_KEY":         "keep",
	})
	if !strings.Contains(text, "ANTHROPIC_API_KEY=a") {
		t.Fatal(text)
	}
	if !strings.Contains(text, "EXTRA_KEY=keep") {
		t.Fatal("missing extra")
	}
}

func TestReadWriteDwarSettingsRoundTrip(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envPath, []byte("ANTHROPIC_API_KEY=old\nKEEP_ME=yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed := dwarSettings{Env: dwarEnvFile{Anthropic: "sk-test", Gemini: "g", OpenAI: "o", Deepgram: "d"}}
	if err := writeDwarSettings(envPath, seed); err != nil {
		t.Fatal(err)
	}
	got, err := readDwarSettings(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.Env != seed.Env {
		t.Fatalf("env round trip: %+v", got.Env)
	}
	body, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "KEEP_ME=yes") {
		t.Fatalf("lost extra key: %s", body)
	}
}
