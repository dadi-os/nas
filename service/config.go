package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Only Dwar has user-editable secrets (provider keys). Yaad/Dimaag Postgres
// credentials are baked into compose/quadlets — not Preferences/.env.
var moduleEnvNames = map[string]struct{}{
	"dwar": {},
}

// ModuleUnit maps API name → systemd unit (prod / DADI_RUNTIME=podman).
var moduleUnit = map[string]string{
	"dwar":        "dwar",
	"yaad":        "yaad",
	"dimaag":      "dimaag",
	"nas":         "nas.service",
	"caddy":       "caddy.service",
	"headscale":   "headscale.service",
	"cloudflared": "cloudflared.service",
	"loki":        "loki.service",
	"alloy":       "alloy.service",
	"tailscale":   "dadi-tailscale.service",
}

// ComposeService maps API name → docker compose service (dev). Empty = no-op.
var composeService = map[string]string{
	"dwar":        "dwar",
	"yaad":        "yaad",
	"dimaag":      "dimaag",
	"nas":         "nas-service",
	"caddy":       "caddy",
	"headscale":   "headscale",
	"cloudflared": "",
	"loki":        "loki",
	"alloy":       "alloy",
	"tailscale":   "tailscale",
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

func (s stateConfig) cloudflaredTokenPath() string {
	return filepath.Join(s.dir, "cloudflared", "token")
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

	mux.HandleFunc("GET /cloudflared/token", func(w http.ResponseWriter, r *http.Request) {
		body, err := os.ReadFile(s.cloudflaredTokenPath())
		if err != nil {
			if os.IsNotExist(err) {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				return
			}
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(body)
	})

	mux.HandleFunc("PUT /cloudflared/token", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "read body")
			return
		}
		path := s.cloudflaredTokenPath()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		token := strings.TrimSpace(string(body))
		if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		if err := s.restartModule("cloudflared"); err != nil {
			slog.Error("restart cloudflared after token write", "code", CodeInternal, "err", err)
			writeError(w, r, http.StatusInternalServerError, CodeInternal, "wrote token but restart failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
			"yaad-postgres", "dimaag-postgres", "headscale.service", "loki.service",
			"yaad-migrate", "dimaag-migrate",
			"yaad", "dimaag", "dwar", "bootstrap.service",
			"nas.service", "caddy.service", "alloy.service",
			"tailscaled.service", "dadi-tailscale.service", "cloudflared.service",
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
			"cloudflared.service", "dadi-tailscale.service", "caddy.service", "nas.service", "alloy.service",
			"dwar", "yaad", "dimaag", "bootstrap.service",
			"yaad-migrate", "dimaag-migrate",
			"yaad-postgres", "dimaag-postgres", "headscale.service", "loki.service",
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
