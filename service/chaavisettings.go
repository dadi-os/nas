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

var chaaviEnvKeys = []string{
	"BW_CLIENTID",
	"BW_CLIENTSECRET",
	"BW_PASSWORD",
}

type chaaviSettings struct {
	Env chaaviEnvFile `json:"env"`
}

type chaaviEnvFile struct {
	ClientID     string `json:"BW_CLIENTID"`
	ClientSecret string `json:"BW_CLIENTSECRET"`
	Password     string `json:"BW_PASSWORD"`
}

func chaaviEnvFromMap(values map[string]string) chaaviEnvFile {
	return chaaviEnvFile{
		ClientID:     values["BW_CLIENTID"],
		ClientSecret: values["BW_CLIENTSECRET"],
		Password:     values["BW_PASSWORD"],
	}
}

func writeChaaviDotEnv(values map[string]string) string {
	var b strings.Builder
	b.WriteString("# Bitwarden personal API key and master password.\n")
	b.WriteString("# Empty is fine — Chaavi boots without them.\n\n")
	written := map[string]struct{}{}
	for _, key := range chaaviEnvKeys {
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

func overlayChaaviEnv(existing map[string]string, env chaaviEnvFile) map[string]string {
	out := map[string]string{}
	for k, v := range existing {
		out[k] = v
	}
	out["BW_CLIENTID"] = env.ClientID
	out["BW_CLIENTSECRET"] = env.ClientSecret
	out["BW_PASSWORD"] = env.Password
	return out
}

func readChaaviSettings(envPath string) (chaaviSettings, error) {
	envBody, err := os.ReadFile(envPath)
	if err != nil {
		return chaaviSettings{}, err
	}
	return chaaviSettings{Env: chaaviEnvFromMap(parseDotEnv(string(envBody)))}, nil
}

func writeChaaviSettings(envPath string, settings chaaviSettings) error {
	existing := map[string]string{}
	if body, err := os.ReadFile(envPath); err == nil {
		existing = parseDotEnv(string(body))
	} else if !os.IsNotExist(err) {
		return err
	}
	envText := writeChaaviDotEnv(overlayChaaviEnv(existing, settings.Env))
	if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(envPath, []byte(envText), 0o600)
}

// registerChaaviSettingsRoutes exposes GET/PUT /modules/chaavi/settings as typed JSON.
// Empty BW_* values are valid; Chaavi reports a missing value on the request that needs it.
func registerChaaviSettingsRoutes(mux *http.ServeMux, s stateConfig) {
	mux.HandleFunc("GET /modules/chaavi/settings", func(w http.ResponseWriter, r *http.Request) {
		st, err := readChaaviSettings(s.moduleEnvPath("chaavi"))
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("PUT /modules/chaavi/settings", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "read body")
			return
		}
		var req chaaviSettings
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
			return
		}
		if err := writeChaaviSettings(s.moduleEnvPath("chaavi"), req); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		if err := s.restartModule("chaavi"); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, "wrote settings but restart failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}
