package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Dwar (provider keys) and Chaavi (BW_*) have user-editable secrets.
// Yaad/Dimaag/Ghar Postgres credentials are baked into compose/quadlets.
var moduleEnvNames = map[string]struct{}{
	"dwar":   {},
	"chaavi": {},
}

// ModuleUnit maps API name → systemd unit (prod / DADI_RUNTIME=podman).
var moduleUnit = map[string]string{
	"dwar":         "dwar",
	"yaad":         "yaad",
	"dimaag":       "dimaag",
	"ghar":         "ghar",
	"chaavi":       "chaavi",
	"chaavi-vault": "chaavi-vault",
	"nas":          "nas.service",
	"caddy":        "caddy.service",
	"headscale":    "headscale.service",
	"loki":         "loki.service",
	"alloy":        "alloy.service",
	"tailscale":    "dadi-tailscale.service",
}

// ComposeService maps API name → docker compose service (dev). Empty = no-op.
var composeService = map[string]string{
	"dwar":         "dwar",
	"yaad":         "yaad",
	"dimaag":       "dimaag",
	"ghar":         "ghar",
	"chaavi":       "chaavi",
	"chaavi-vault": "chaavi-vault",
	"nas":          "nas-service",
	"caddy":        "caddy",
	"headscale":    "headscale",
	"loki":         "loki",
	"alloy":        "alloy",
	"tailscale":    "tailscale",
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

func registerConfigRoutes(mux *http.ServeMux, s stateConfig) {
	updates := &updater{
		run:    s.pullUpdates,
		reboot: func() error { return runCmd("systemctl", "reboot") },
		last:   updateRun{State: "idle"},
	}

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
		if s.runtime == "compose" && scope != "modules" {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "bootc upgrade not available under compose")
			return
		}
		run, started := updates.start(scope)
		if !started {
			writeError(w, r, http.StatusConflict, CodeBusy, "an update is already running (scope "+run.Scope+")")
			return
		}
		writeJSON(w, http.StatusAccepted, run)
	})

	mux.HandleFunc("GET /pull_updates", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, updates.snapshot())
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
		if _, err := s.mintControlURL(); err != nil {
			if s.runtime != "podman" {
				writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, err.Error())
				return
			}
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
			"yaad-postgres", "dimaag-postgres", "ghar-postgres", "chaavi-vault", "headscale.service", "loki.service",
			"yaad-migrate", "dimaag-migrate", "ghar-migrate",
			"yaad", "dimaag", "dwar", "ghar", "chaavi", "bootstrap.service",
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
			"dwar", "yaad", "dimaag", "ghar", "chaavi", "bootstrap.service",
			"yaad-migrate", "dimaag-migrate", "ghar-migrate",
			"yaad-postgres", "dimaag-postgres", "ghar-postgres", "chaavi-vault", "headscale.service", "loki.service",
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

// updateRun is the most recent pull_updates run. State is idle (never run), running,
// succeeded, rebooting (a staged OS deployment is being booted into), or failed.
type updateRun struct {
	State          string `json:"state"`
	Scope          string `json:"scope,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	FinishedAt     string `json:"finished_at,omitempty"`
	RebootRequired bool   `json:"reboot_required"`
	Error          string `json:"error,omitempty"`
}

// updater runs pull_updates in the background so callers are not tied to modules
// (Dimaag included) that podman auto-update restarts mid-run. One run at a time.
// When run reports a reboot is required, reboot is called after the run settles.
type updater struct {
	run    func(scope string) (bool, error)
	reboot func() error
	mu     sync.Mutex
	last   updateRun
}

// start launches a run for scope and returns it. When a run is already in flight it
// returns that run and false.
func (u *updater) start(scope string) (updateRun, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.last.State == "running" {
		return u.last, false
	}
	u.last = updateRun{State: "running", Scope: scope, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	go u.finish(scope)
	return u.last, true
}

func (u *updater) finish(scope string) {
	rebootRequired, err := u.run(scope)
	u.settle(rebootRequired, err)
	if err != nil {
		slog.Error("pull_updates failed", "code", CodeInternal, "scope", scope, "err", err)
		return
	}
	slog.Info("pull_updates succeeded", "scope", scope, "reboot_required", rebootRequired)
	if !rebootRequired {
		return
	}
	if err := u.reboot(); err != nil {
		u.settle(true, fmt.Errorf("reboot: %w", err))
		slog.Error("pull_updates reboot failed", "code", CodeInternal, "scope", scope, "err", err)
	}
}

func (u *updater) settle(rebootRequired bool, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.last.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	u.last.RebootRequired = rebootRequired
	switch {
	case err != nil:
		u.last.State = "failed"
		u.last.Error = err.Error()
	case rebootRequired:
		u.last.State = "rebooting"
	default:
		u.last.State = "succeeded"
	}
}

func (u *updater) snapshot() updateRun {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.last
}

// pullUpdates applies module and/or OS updates. scope must be modules, os, or all
// (compose supports modules only). Returns whether a reboot is required (bootc staged
// a new deployment). Does not reboot.
func (s stateConfig) pullUpdates(scope string) (rebootRequired bool, err error) {
	switch s.runtime {
	case "compose":
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
