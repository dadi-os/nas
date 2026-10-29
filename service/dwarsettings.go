package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var dwarEnvKeys = []string{
	"ANTHROPIC_API_KEY",
	"GEMINI_API_KEY",
	"OPENAI_API_KEY",
	"DEEPGRAM_API_KEY",
}

// dwarSettings is what Preferences edits for Dwar: its provider keys. Models, budgets and
// limits are constants in Dwar's own config.py, so changing one is a push to Dwar.
type dwarSettings struct {
	Env dwarEnvFile `json:"env"`
}

type dwarEnvFile struct {
	Anthropic string `json:"ANTHROPIC_API_KEY"`
	Gemini    string `json:"GEMINI_API_KEY"`
	OpenAI    string `json:"OPENAI_API_KEY"`
	Deepgram  string `json:"DEEPGRAM_API_KEY"`
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

// envFileFromMap picks the provider key variables out of a parsed Dwar .env.
func envFileFromMap(values map[string]string) dwarEnvFile {
	return dwarEnvFile{
		Anthropic: values["ANTHROPIC_API_KEY"],
		Gemini:    values["GEMINI_API_KEY"],
		OpenAI:    values["OPENAI_API_KEY"],
		Deepgram:  values["DEEPGRAM_API_KEY"],
	}
}

// writeDotEnv renders values as Dwar's .env: the provider keys first in a fixed order, then
// any other keys sorted, so hand-added keys survive a settings save.
func writeDotEnv(values map[string]string) string {
	var b strings.Builder
	b.WriteString("# Provider credentials only. Model IDs and token budgets live in Dwar's config.py.\n")
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

// overlayEnv returns a copy of existing with the provider keys replaced by env.
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

// readDwarSettings reads Dwar's provider keys from its .env at envPath.
func readDwarSettings(envPath string) (dwarSettings, error) {
	body, err := os.ReadFile(envPath)
	if err != nil {
		return dwarSettings{}, err
	}
	return dwarSettings{Env: envFileFromMap(parseDotEnv(string(body)))}, nil
}

// writeDwarSettings writes Dwar's .env (0600) with settings.Env, keeping keys the settings do not cover.
func writeDwarSettings(envPath string, settings dwarSettings) error {
	existing := map[string]string{}
	if body, err := os.ReadFile(envPath); err == nil {
		existing = parseDotEnv(string(body))
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(envPath, []byte(writeDotEnv(overlayEnv(existing, settings.Env))), 0o600)
}

// registerDwarSettingsRoutes exposes GET/PUT /modules/dwar/settings: Dwar's provider keys as typed JSON.
func registerDwarSettingsRoutes(mux *http.ServeMux, s stateConfig) {
	mux.HandleFunc("GET /modules/dwar/settings", func(w http.ResponseWriter, r *http.Request) {
		st, err := readDwarSettings(s.moduleEnvPath("dwar"))
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
		if err := writeDwarSettings(s.moduleEnvPath("dwar"), req); err != nil {
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
