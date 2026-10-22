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
	"CHAAVI_EMAIL",
	"CHAAVI_PASSWORD",
}

// legacyChaaviEnvKeys are the adapter's old keys. A saved .env drops them; until then an old
// BW_PASSWORD still stands in for an empty CHAAVI_PASSWORD, as it does inside Chaavi.
var legacyChaaviEnvKeys = []string{
	"BW_CLIENTID",
	"BW_CLIENTSECRET",
	"BW_PASSWORD",
}

type chaaviSettings struct {
	Env chaaviEnvFile `json:"env"`
}

type chaaviEnvFile struct {
	Email    string `json:"CHAAVI_EMAIL"`
	Password string `json:"CHAAVI_PASSWORD"`
}

// chaaviEnvFromMap picks the CHAAVI_* keys out of a parsed Chaavi .env.
func chaaviEnvFromMap(values map[string]string) chaaviEnvFile {
	password := values["CHAAVI_PASSWORD"]
	if password == "" {
		password = values["BW_PASSWORD"]
	}
	return chaaviEnvFile{
		Email:    values["CHAAVI_EMAIL"],
		Password: password,
	}
}

// writeChaaviDotEnv renders values as Chaavi's .env: the CHAAVI_* keys first in a fixed order,
// then any other keys sorted, so hand-added keys survive a settings save.
func writeChaaviDotEnv(values map[string]string) string {
	var b strings.Builder
	b.WriteString("# The vault account Chaavi unlocks: its email and master password.\n")
	b.WriteString("# Empty is fine — Chaavi boots without them; with one account it finds the email itself.\n\n")
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

// overlayChaaviEnv returns a copy of existing with the CHAAVI_* keys replaced by env and the
// adapter's old BW_* keys removed.
func overlayChaaviEnv(existing map[string]string, env chaaviEnvFile) map[string]string {
	out := map[string]string{}
	for k, v := range existing {
		out[k] = v
	}
	for _, key := range legacyChaaviEnvKeys {
		delete(out, key)
	}
	out["CHAAVI_EMAIL"] = env.Email
	out["CHAAVI_PASSWORD"] = env.Password
	return out
}

// readChaaviSettings reads Chaavi's .env at envPath into chaaviSettings.
func readChaaviSettings(envPath string) (chaaviSettings, error) {
	envBody, err := os.ReadFile(envPath)
	if err != nil {
		return chaaviSettings{}, err
	}
	return chaaviSettings{Env: chaaviEnvFromMap(parseDotEnv(string(envBody)))}, nil
}

// writeChaaviSettings writes settings to Chaavi's .env at envPath (0600), keeping keys the
// settings do not cover. A missing .env is created.
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
// Empty CHAAVI_* values are valid; Chaavi reports a missing value on the request that needs it.
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
