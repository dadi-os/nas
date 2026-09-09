package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
)

const (
	defaultRunDir  = "/run/dadi"
	dadiUsername   = "dadi"
	tmuxSocketName = "tmux.sock"
)

type hostRuntime struct {
	stateDir    string
	projectsDir string
	browsersDir string
	runDir      string
	tmuxSocket  string
	runtime     string // "podman" | "compose"
	switchUser  bool
	dadiUID     int
	dadiGID     int
	idleShell   string
}

func newHostRuntime(state stateConfig) (*hostRuntime, error) {
	h := &hostRuntime{
		stateDir:    state.dir,
		projectsDir: filepath.Join(state.dir, "projects"),
		browsersDir: filepath.Join(state.dir, "browsers"),
		runtime:     state.runtime,
		// Appliance runs Nas as root and switches to dadi. Compose/dev skips.
		switchUser: state.runtime == "podman",
		idleShell:  "bash",
		dadiUID:    -1,
		dadiGID:    -1,
	}
	if !h.switchUser {
		if shell := os.Getenv("SHELL"); shell != "" {
			h.idleShell = filepath.Base(shell)
		}
	}
	if h.switchUser {
		u, err := user.Lookup(dadiUsername)
		if err != nil {
			return nil, fmt.Errorf("lookup %s: %w", dadiUsername, err)
		}
		uid, err := strconv.Atoi(u.Uid)
		if err != nil {
			return nil, fmt.Errorf("parse uid: %w", err)
		}
		gid, err := strconv.Atoi(u.Gid)
		if err != nil {
			return nil, fmt.Errorf("parse gid: %w", err)
		}
		h.dadiUID = uid
		h.dadiGID = gid
	}
	if err := h.ensureDirs(); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *hostRuntime) ensureDirs() error {
	runDir := defaultRunDir
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		// Non-root / read-only /run (dev Mac, CI): keep the socket under state.
		runDir = filepath.Join(h.stateDir, "run")
		if err := os.MkdirAll(runDir, 0o755); err != nil {
			return fmt.Errorf("mkdir run dir: %w", err)
		}
	}
	h.runDir = runDir
	h.tmuxSocket = filepath.Join(runDir, tmuxSocketName)
	// macOS AF_UNIX path limit is short; fall back to /tmp when needed.
	if len(h.tmuxSocket) > 100 {
		h.runDir = filepath.Join(os.TempDir(), fmt.Sprintf("dadi-tmux-%d", os.Getpid()))
		if err := os.MkdirAll(h.runDir, 0o755); err != nil {
			return fmt.Errorf("mkdir short run dir: %w", err)
		}
		h.tmuxSocket = filepath.Join(h.runDir, tmuxSocketName)
	}

	for _, dir := range []string{h.projectsDir, h.browsersDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			if dir == h.projectsDir {
				h.projectsDir = resolved
			} else {
				h.browsersDir = resolved
			}
		}
		if err := h.chownDadi(dir); err != nil {
			return err
		}
	}
	if h.switchUser {
		if err := os.Chown(h.runDir, h.dadiUID, h.dadiGID); err != nil {
			return fmt.Errorf("chown run dir: %w", err)
		}
	}
	return nil
}

func (h *hostRuntime) chownDadi(path string) error {
	if !h.switchUser {
		return nil
	}
	return os.Chown(path, h.dadiUID, h.dadiGID)
}

// command returns an *exec.Cmd that runs as dadi on the appliance.
func (h *hostRuntime) command(name string, args ...string) *exec.Cmd {
	if h.switchUser {
		full := append([]string{"-u", dadiUsername, "--", name}, args...)
		return exec.Command("runuser", full...)
	}
	return exec.Command(name, args...)
}
