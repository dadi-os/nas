package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

var dwarEnvKeys = []string{
	"ANTHROPIC_API_KEY",
	"GEMINI_API_KEY",
	"OPENAI_API_KEY",
	"DEEPGRAM_API_KEY",
}

type dwarSettings struct {
	Env    dwarEnvFile    `json:"env"`
	Config dwarConfigFile `json:"config"`
}

type dwarEnvFile struct {
	Anthropic string `json:"ANTHROPIC_API_KEY"`
	Gemini    string `json:"GEMINI_API_KEY"`
	OpenAI    string `json:"OPENAI_API_KEY"`
	Deepgram  string `json:"DEEPGRAM_API_KEY"`
}

type dwarChatReasoning struct {
	Provider       string `json:"provider" toml:"provider"`
	Model          string `json:"model" toml:"model"`
	MaxTokens      int    `json:"max_tokens" toml:"max_tokens"`
	ThinkingBudget int    `json:"thinking_budget" toml:"thinking_budget"`
}

type dwarChatLane struct {
	Provider  string `json:"provider" toml:"provider"`
	Model     string `json:"model" toml:"model"`
	MaxTokens int    `json:"max_tokens" toml:"max_tokens"`
}

type dwarEmbed struct {
	Provider      string `json:"provider" toml:"provider"`
	Model         string `json:"model" toml:"model"`
	Dimensions    int    `json:"dimensions" toml:"dimensions"`
	MaxBatchSize  int    `json:"max_batch_size" toml:"max_batch_size"`
	MaxTextLength int    `json:"max_text_length" toml:"max_text_length"`
}

type dwarImageDescribe struct {
	Provider          string   `json:"provider" toml:"provider"`
	Model             string   `json:"model" toml:"model"`
	MaxTokens         int      `json:"max_tokens" toml:"max_tokens"`
	MaxBytes          int      `json:"max_bytes" toml:"max_bytes"`
	MaxPromptLength   int      `json:"max_prompt_length" toml:"max_prompt_length"`
	AllowedMediaTypes []string `json:"allowed_media_types" toml:"allowed_media_types"`
}

type dwarImageCreate struct {
	Provider        string `json:"provider" toml:"provider"`
	Model           string `json:"model" toml:"model"`
	MaxPromptLength int    `json:"max_prompt_length" toml:"max_prompt_length"`
}

type dwarSpeech struct {
	Provider          string   `json:"provider" toml:"provider"`
	Model             string   `json:"model" toml:"model"`
	Language          string   `json:"language" toml:"language"`
	MaxBytes          int      `json:"max_bytes" toml:"max_bytes"`
	AllowedMediaTypes []string `json:"allowed_media_types" toml:"allowed_media_types"`
}

type dwarRetry struct {
	Attempts       int       `json:"attempts" toml:"attempts"`
	BackoffSeconds []float64 `json:"backoff_seconds" toml:"backoff_seconds"`
	TimeoutSeconds int       `json:"timeout_seconds" toml:"timeout_seconds"`
}

type dwarConfigFile struct {
	Chat struct {
		Reasoning    dwarChatReasoning `json:"reasoning" toml:"reasoning"`
		Conversation dwarChatLane      `json:"conversation" toml:"conversation"`
	} `json:"chat" toml:"chat"`
	Embed dwarEmbed `json:"embed" toml:"embed"`
	Image struct {
		Describe dwarImageDescribe `json:"describe" toml:"describe"`
		Create   dwarImageCreate   `json:"create" toml:"create"`
	} `json:"image" toml:"image"`
	Speech struct {
		Transcribe dwarSpeech `json:"transcribe" toml:"transcribe"`
	} `json:"speech" toml:"speech"`
	Retry dwarRetry `json:"retry" toml:"retry"`
}

// parseDotEnv reads KEY=VALUE lines and ignores comments.
func parseDotEnv(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		key, value, ok := strings.Cut(trim, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		out[key] = value
	}
	return out
}

func envFileFromMap(values map[string]string) dwarEnvFile {
	return dwarEnvFile{
		Anthropic: values["ANTHROPIC_API_KEY"],
		Gemini:    values["GEMINI_API_KEY"],
		OpenAI:    values["OPENAI_API_KEY"],
		Deepgram:  values["DEEPGRAM_API_KEY"],
	}
}

func writeDotEnv(values map[string]string) string {
	var b strings.Builder
	b.WriteString("# Provider credentials only. Model IDs and token budgets live in config.toml.\n")
	b.WriteString("# Empty is fine — Dwar boots without keys.\n\n")
	written := map[string]struct{}{}
	for _, key := range dwarEnvKeys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(values[key])
		b.WriteByte('\n')
		written[key] = struct{}{}
	}
	var extras []string
	for key := range values {
		if _, ok := written[key]; !ok {
			extras = append(extras, key)
		}
	}
	if len(extras) > 0 {
		slices.Sort(extras)
		b.WriteByte('\n')
		for _, key := range extras {
			b.WriteString(key)
			b.WriteByte('=')
			b.WriteString(values[key])
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func overlayEnv(existing map[string]string, env dwarEnvFile) map[string]string {
	out := map[string]string{}
	for k, v := range existing {
		out[k] = v
	}
	out["ANTHROPIC_API_KEY"] = env.Anthropic
	out["GEMINI_API_KEY"] = env.Gemini
	out["OPENAI_API_KEY"] = env.OpenAI
	out["DEEPGRAM_API_KEY"] = env.Deepgram
	return out
}

// validateDwarSettings rejects empty providers/models and non-positive numeric fields.
func validateDwarSettings(s dwarSettings) error {
	c := s.Config
	if c.Chat.Reasoning.Provider == "" || c.Chat.Reasoning.Model == "" {
		return fmt.Errorf("chat.reasoning provider and model are required")
	}
	if c.Chat.Reasoning.MaxTokens < 1 {
		return fmt.Errorf("chat.reasoning max_tokens must be >= 1")
	}
	if c.Chat.Reasoning.ThinkingBudget < 0 {
		return fmt.Errorf("chat.reasoning thinking_budget must be >= 0")
	}
	if c.Chat.Conversation.Provider == "" || c.Chat.Conversation.Model == "" {
		return fmt.Errorf("chat.conversation provider and model are required")
	}
	if c.Chat.Conversation.MaxTokens < 1 {
		return fmt.Errorf("chat.conversation max_tokens must be >= 1")
	}
	if c.Embed.Provider == "" || c.Embed.Model == "" {
		return fmt.Errorf("embed provider and model are required")
	}
	if c.Embed.Dimensions < 1 || c.Embed.MaxBatchSize < 1 || c.Embed.MaxTextLength < 1 {
		return fmt.Errorf("embed dimensions, max_batch_size, and max_text_length must be >= 1")
	}
	if c.Image.Describe.Provider == "" || c.Image.Describe.Model == "" {
		return fmt.Errorf("image.describe provider and model are required")
	}
	if c.Image.Describe.MaxTokens < 1 || c.Image.Describe.MaxBytes < 1 || c.Image.Describe.MaxPromptLength < 1 {
		return fmt.Errorf("image.describe max_tokens, max_bytes, and max_prompt_length must be >= 1")
	}
	if len(c.Image.Describe.AllowedMediaTypes) == 0 {
		return fmt.Errorf("image.describe allowed_media_types is required")
	}
	if c.Image.Create.Provider == "" || c.Image.Create.Model == "" {
		return fmt.Errorf("image.create provider and model are required")
	}
	if c.Image.Create.MaxPromptLength < 1 {
		return fmt.Errorf("image.create max_prompt_length must be >= 1")
	}
	if c.Speech.Transcribe.Provider == "" || c.Speech.Transcribe.Model == "" || c.Speech.Transcribe.Language == "" {
		return fmt.Errorf("speech.transcribe provider, model, and language are required")
	}
	if c.Speech.Transcribe.MaxBytes < 1 {
		return fmt.Errorf("speech.transcribe max_bytes must be >= 1")
	}
	if len(c.Speech.Transcribe.AllowedMediaTypes) == 0 {
		return fmt.Errorf("speech.transcribe allowed_media_types is required")
	}
	if c.Retry.Attempts < 1 {
		return fmt.Errorf("retry.attempts must be >= 1")
	}
	if len(c.Retry.BackoffSeconds) == 0 {
		return fmt.Errorf("retry.backoff_seconds is required")
	}
	for _, b := range c.Retry.BackoffSeconds {
		if b < 0 {
			return fmt.Errorf("retry.backoff_seconds must be >= 0")
		}
	}
	if c.Retry.TimeoutSeconds < 1 {
		return fmt.Errorf("retry.timeout_seconds must be >= 1")
	}
	return nil
}

func readDwarSettings(envPath, configPath string) (dwarSettings, error) {
	envBody, err := os.ReadFile(envPath)
	if err != nil {
		return dwarSettings{}, err
	}
	cfgBody, err := os.ReadFile(configPath)
	if err != nil {
		return dwarSettings{}, err
	}
	var cfg dwarConfigFile
	if err := toml.Unmarshal(cfgBody, &cfg); err != nil {
		return dwarSettings{}, fmt.Errorf("parse config.toml: %w", err)
	}
	return dwarSettings{
		Env:    envFileFromMap(parseDotEnv(string(envBody))),
		Config: cfg,
	}, nil
}

func writeDwarSettings(envPath, configPath string, settings dwarSettings) error {
	existing := map[string]string{}
	if body, err := os.ReadFile(envPath); err == nil {
		existing = parseDotEnv(string(body))
	} else if !os.IsNotExist(err) {
		return err
	}
	envText := writeDotEnv(overlayEnv(existing, settings.Env))
	cfgBody, err := toml.Marshal(settings.Config)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(envPath, []byte(envText), 0o600); err != nil {
		return err
	}
	return os.WriteFile(configPath, cfgBody, 0o644)
}

// registerDwarSettingsRoutes exposes GET/PUT /modules/dwar/settings as typed JSON.
func registerDwarSettingsRoutes(mux *http.ServeMux, s stateConfig) {
	mux.HandleFunc("GET /modules/dwar/settings", func(w http.ResponseWriter, r *http.Request) {
		st, err := readDwarSettings(s.moduleEnvPath("dwar"), s.dwarConfigPath())
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("PUT /modules/dwar/settings", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "read body")
			return
		}
		var req dwarSettings
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
			return
		}
		if err := validateDwarSettings(req); err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, err.Error())
			return
		}
		if err := writeDwarSettings(s.moduleEnvPath("dwar"), s.dwarConfigPath(), req); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		if err := s.restartModule("dwar"); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, "wrote settings but restart failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}
