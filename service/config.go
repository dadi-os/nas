package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// errControlURLUnset means Preferences → Tunnel has no control plane URL yet.
var errControlURLUnset = errors.New("control URL not set — configure in Preferences → Tunnel")

// Only Dwar has user-editable secrets (provider keys). Yaad/Dimaag Postgres
// credentials are baked into compose/quadlets — not Preferences/.env.
var moduleEnvNames = map[string]struct{}{
	"dwar": {},
}

// ModuleUnit maps API name → systemd unit (prod / DADI_RUNTIME=podman).
var moduleUnit = map[string]string{
	"dwar":      "dwar",
	"yaad":      "yaad",
	"dimaag":    "dimaag",
	"ghar":      "ghar",
	"nas":       "nas.service",
	"caddy":     "caddy.service",
	"headscale": "headscale.service",
	"loki":      "loki.service",
	"alloy":     "alloy.service",
	"tailscale": "dadi-tailscale.service",
}

// ComposeService maps API name → docker compose service (dev). Empty = no-op.
var composeService = map[string]string{
	"dwar":      "dwar",
	"yaad":      "yaad",
	"dimaag":    "dimaag",
	"ghar":      "ghar",
	"nas":       "nas-service",
	"caddy":     "caddy",
	"headscale": "headscale",
	"loki":      "loki",
	"alloy":     "alloy",
	"tailscale": "tailscale",
}

type stateConfig struct {
	dir        string
	runtime    string // "podman" | "compose"
	composeDir string // required when runtime=compose
}

func loadStateConfig() (stateConfig, error) {
	dir := os.Getenv("DADI_STATE_DIR")
	if dir == "" {
		return stateConfig{}, fmt.Errorf("DADI_STATE_DIR is required")
	}
	runtime := os.Getenv("DADI_RUNTIME")
	if runtime == "" {
		return stateConfig{}, fmt.Errorf("DADI_RUNTIME is required (podman or compose)")
	}
	cfg := stateConfig{dir: dir, runtime: runtime}
	switch runtime {
	case "podman":
	case "compose":
		cfg.composeDir = os.Getenv("DADI_COMPOSE_DIR")
		if cfg.composeDir == "" {
			return stateConfig{}, fmt.Errorf("DADI_COMPOSE_DIR is required when DADI_RUNTIME=compose")
		}
	default:
		return stateConfig{}, fmt.Errorf("DADI_RUNTIME must be podman or compose")
	}
	return cfg, nil
}

func (s stateConfig) moduleEnvPath(name string) string {
	return filepath.Join(s.dir, "modules", name, ".env")
}

func (s stateConfig) dwarConfigPath() string {
	return filepath.Join(s.dir, "modules", "dwar", "config.toml")
}

func (s stateConfig) controlURLPath() string {
	return filepath.Join(s.dir, "headscale", "control_url")
}

// seedControlURL writes CONTROL_URL into the preference file once when the
// file is missing or empty. After that the file is the sole source of truth.
func (s stateConfig) seedControlURL(envSeed string) error {
	path := s.controlURLPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(body)) != "" {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	seed := strings.TrimSpace(envSeed)
	if seed == "" {
		if os.IsNotExist(err) {
			return os.WriteFile(path, []byte{}, 0o600)
		}
		return nil
	}
	return os.WriteFile(path, []byte(seed+"\n"), 0o600)
}

// resolveControlURL reads the on-disk preference; empty or missing is an error.
func (s stateConfig) resolveControlURL() (string, error) {
	body, err := os.ReadFile(s.controlURLPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", errControlURLUnset
		}
		return "", err
	}
	u := strings.TrimSpace(string(body))
	if u == "" {
		return "", errControlURLUnset
	}
	return u, nil
}

func registerConfigRoutes(mux *http.ServeMux, s stateConfig) {
	mux.HandleFunc("GET /modules/{name}/env", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if _, ok := moduleEnvNames[name]; !ok {
			writeError(w, r, http.StatusNotFound, CodeNotFound, "unknown module")
			return
		}
		body, err := os.ReadFile(s.moduleEnvPath(name))
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(body)
	})

	mux.HandleFunc("PUT /modules/{name}/env", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if _, ok := moduleEnvNames[name]; !ok {
			writeError(w, r, http.StatusNotFound, CodeNotFound, "unknown module")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "read body")
			return
		}
		path := s.moduleEnvPath(name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		if err := s.restartModule(name); err != nil {
			slog.Error("restart after env write", "code", CodeInternal, "module", name, "err", err)
			writeError(w, r, http.StatusInternalServerError, CodeInternal, "wrote env but restart failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /modules/dwar/config", func(w http.ResponseWriter, r *http.Request) {
		body, err := os.ReadFile(s.dwarConfigPath())
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(body)
	})

	mux.HandleFunc("PUT /modules/dwar/config", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "read body")
			return
		}
		path := s.dwarConfigPath()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		if err := s.restartModule("dwar"); err != nil {
			slog.Error("restart after config write", "code", CodeInternal, "err", err)
			writeError(w, r, http.StatusInternalServerError, CodeInternal, "wrote config but restart failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /modules/{name}/restart", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if _, ok := moduleUnit[name]; !ok {
			writeError(w, r, http.StatusNotFound, CodeNotFound, "unknown module")
			return
		}
		if err := s.restartModule(name); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /stack/up", func(w http.ResponseWriter, r *http.Request) {
		if err := s.stackUp(); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /stack/down", func(w http.ResponseWriter, r *http.Request) {
		if err := s.stackDown(); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /pull_updates", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Scope string `json:"scope"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&req); err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
			return
		}
		scope := strings.TrimSpace(req.Scope)
		if scope != "modules" && scope != "os" && scope != "all" {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "scope must be modules, os, or all")
			return
		}
		rebootRequired, err := s.pullUpdates(scope)
		if err != nil {
			if strings.Contains(err.Error(), "not available under compose") {
				writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, err.Error())
				return
			}
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":          "ok",
			"scope":           scope,
			"reboot_required": rebootRequired,
		})
	})

	mux.HandleFunc("GET /headscale/control-url", func(w http.ResponseWriter, r *http.Request) {
		body, err := os.ReadFile(s.controlURLPath())
		if err != nil {
			if os.IsNotExist(err) {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				return
			}
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(strings.TrimSpace(string(body))))
	})

	mux.HandleFunc("PUT /headscale/control-url", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<12))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "read body")
			return
		}
		url := strings.TrimSpace(string(body))
		if err := validateControlURL(s.runtime, url); err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, err.Error())
			return
		}
		path := s.controlURLPath()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		if err := os.WriteFile(path, []byte(url+"\n"), 0o600); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		if s.runtime == "podman" {
			if err := s.applyControlPlanePublish(url); err != nil {
				slog.Error("publish control plane", "code", CodeInternal, "err", err)
				writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
				return
			}
			st, err := s.loadPublishStatus()
			if err != nil {
				writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"status":      "ok",
				"control_url": st.ControlURL,
				"hostname":    st.Hostname,
				"lan_ip":      st.LANIP,
				"wan_ip":      st.WANIP,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /headscale/publish", func(w http.ResponseWriter, r *http.Request) {
		st, err := s.loadPublishStatus()
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("POST /headscale/publish", func(w http.ResponseWriter, r *http.Request) {
		if s.runtime != "podman" {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "control plane publish is appliance-only")
			return
		}
		if _, err := s.resolveControlURL(); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeConfigMissing, err.Error())
			return
		}
		if err := s.mapHeadscalePorts(); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		st, err := s.loadPublishStatus()
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})
}

func (s stateConfig) restartModule(name string) error {
	switch s.runtime {
	case "podman":
		unit, ok := moduleUnit[name]
		if !ok {
			return fmt.Errorf("unknown module %q", name)
		}
		return hostSystemctl("restart", unit)
	case "compose":
		svc, ok := composeService[name]
		if !ok {
			return fmt.Errorf("unknown module %q", name)
		}
		if svc == "" {
			return nil
		}
		return s.composeCmd("restart", svc)
	default:
		return fmt.Errorf("unsupported runtime %q", s.runtime)
	}
}

func (s stateConfig) stackUp() error {
	switch s.runtime {
	case "podman":
		_ = hostSystemctl("start", "dadi-seed.service")
		units := []string{
			"yaad-postgres", "dimaag-postgres", "ghar-postgres", "headscale.service", "loki.service",
			"yaad-migrate", "dimaag-migrate", "ghar-migrate",
			"yaad", "dimaag", "dwar", "ghar", "bootstrap.service",
			"nas.service", "caddy.service", "alloy.service",
			"tailscaled.service", "dadi-tailscale.service",
		}
		for _, u := range units {
			if err := hostSystemctl("start", u); err != nil {
				return fmt.Errorf("start %s: %w", u, err)
			}
		}
		return nil
	case "compose":
		return s.composeCmd("up", "-d", "--build")
	default:
		return fmt.Errorf("unsupported runtime %q", s.runtime)
	}
}

func (s stateConfig) stackDown() error {
	switch s.runtime {
	case "podman":
		units := []string{
			"dadi-tailscale.service", "caddy.service", "nas.service", "alloy.service",
			"dwar", "yaad", "dimaag", "ghar", "bootstrap.service",
			"yaad-migrate", "dimaag-migrate", "ghar-migrate",
			"yaad-postgres", "dimaag-postgres", "ghar-postgres", "headscale.service", "loki.service",
		}
		for _, u := range units {
			_ = hostSystemctl("stop", u)
		}
		return nil
	case "compose":
		return s.composeCmd("down")
	default:
		return fmt.Errorf("unsupported runtime %q", s.runtime)
	}
}

// pullUpdates applies module and/or OS updates. scope must be modules, os, or all.
// Returns whether a reboot is required (bootc staged a new deployment). Does not reboot.
func (s stateConfig) pullUpdates(scope string) (rebootRequired bool, err error) {
	switch s.runtime {
	case "compose":
		if scope == "os" || scope == "all" {
			return false, fmt.Errorf("bootc upgrade not available under compose")
		}
		if err := s.composeCmd("pull"); err != nil {
			return false, err
		}
		return false, s.composeCmd("up", "-d")
	case "podman":
		if scope == "modules" || scope == "all" {
			if err := runCmd("podman", "auto-update"); err != nil {
				return false, err
			}
		}
		if scope == "os" || scope == "all" {
			out, runErr := exec.Command("bootc", "upgrade").CombinedOutput()
			if runErr != nil {
				return false, fmt.Errorf("bootc upgrade: %w (%s)", runErr, strings.TrimSpace(string(out)))
			}
			combined := string(out)
			rebootRequired = strings.Contains(combined, "Queued for next boot") ||
				strings.Contains(combined, "staged") ||
				strings.Contains(combined, "Changes queued")
			statusOut, statusErr := exec.Command("bootc", "status", "--json").CombinedOutput()
			if statusErr == nil && strings.Contains(string(statusOut), `"staged"`) &&
				!strings.Contains(string(statusOut), `"staged": null`) &&
				!strings.Contains(string(statusOut), `"staged":null`) {
				rebootRequired = true
			}
		}
		return rebootRequired, nil
	default:
		return false, fmt.Errorf("unsupported runtime %q", s.runtime)
	}
}

func hostSystemctl(verb string, unit string) error {
	return runCmd("systemctl", verb, unit)
}

func (s stateConfig) composeCmd(args ...string) error {
	full := append([]string{
		"compose",
		"-f", filepath.Join(s.composeDir, "docker-compose.yml"),
		"--project-directory", s.composeDir,
	}, args...)
	return runCmd("docker", full...)
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
