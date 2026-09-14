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

func validSettings() dwarSettings {
	s := dwarSettings{}
	s.Env.Anthropic = "sk-test"
	s.Config.Chat.Reasoning.Provider = "anthropic"
	s.Config.Chat.Reasoning.Model = "claude-sonnet-5"
	s.Config.Chat.Reasoning.MaxTokens = 16384
	s.Config.Chat.Reasoning.ThinkingBudget = 4096
	s.Config.Chat.Conversation.Provider = "gemini"
	s.Config.Chat.Conversation.Model = "gemini-3.6-flash"
	s.Config.Chat.Conversation.MaxTokens = 4096
	s.Config.Embed.Provider = "openai"
	s.Config.Embed.Model = "text-embedding-3-small"
	s.Config.Embed.Dimensions = 1536
	s.Config.Embed.MaxBatchSize = 64
	s.Config.Embed.MaxTextLength = 8192
	s.Config.Image.Describe.Provider = "gemini"
	s.Config.Image.Describe.Model = "gemini-3.6-flash"
	s.Config.Image.Describe.MaxTokens = 2048
	s.Config.Image.Describe.MaxBytes = 10485760
	s.Config.Image.Describe.MaxPromptLength = 4000
	s.Config.Image.Describe.AllowedMediaTypes = []string{"image/jpeg", "image/png"}
	s.Config.Image.Create.Provider = "gemini"
	s.Config.Image.Create.Model = "gemini-3.1-flash-image"
	s.Config.Image.Create.MaxPromptLength = 4000
	s.Config.Speech.Transcribe.Provider = "deepgram"
	s.Config.Speech.Transcribe.Model = "nova-3"
	s.Config.Speech.Transcribe.Language = "multi"
	s.Config.Speech.Transcribe.MaxBytes = 26214400
	s.Config.Speech.Transcribe.AllowedMediaTypes = []string{"audio/wav"}
	s.Config.Retry.Attempts = 3
	s.Config.Retry.BackoffSeconds = []float64{0.5, 1, 2}
	s.Config.Retry.TimeoutSeconds = 300
	return s
}

func TestValidateDwarSettings(t *testing.T) {
	s := validSettings()
	if err := validateDwarSettings(s); err != nil {
		t.Fatalf("valid: %v", err)
	}
	s.Config.Chat.Reasoning.MaxTokens = 0
	if err := validateDwarSettings(s); err == nil {
		t.Fatal("expected max_tokens error")
	}
}

func TestReadWriteDwarSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	cfgPath := filepath.Join(dir, "config.toml")
	seed := validSettings()
	if err := os.WriteFile(envPath, []byte("ANTHROPIC_API_KEY=old\nKEEP_ME=yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDwarSettings(envPath, cfgPath, seed); err != nil {
		t.Fatal(err)
	}
	got, err := readDwarSettings(envPath, cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.Env.Anthropic != "sk-test" {
		t.Fatalf("env overlay: %+v", got.Env)
	}
	body, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "KEEP_ME=yes") {
		t.Fatalf("lost extra key: %s", body)
	}
	if got.Config.Chat.Reasoning.Model != "claude-sonnet-5" {
		t.Fatalf("config: %+v", got.Config.Chat.Reasoning)
	}
	if got.Config.Retry.TimeoutSeconds != 300 {
		t.Fatalf("retry: %+v", got.Config.Retry)
	}
}
