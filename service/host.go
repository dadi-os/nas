package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	defaultRunDir  = "/run/dadi"
	dadiUsername   = "dadi"
	tmuxSocketName = "tmux.sock"
)

type hostRuntime struct {
	stateDir    string
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
	stateDir := state.dir
	if resolved, err := filepath.EvalSymlinks(stateDir); err == nil {
		stateDir = resolved
	}
	h := &hostRuntime{
		stateDir:    stateDir,
		browsersDir: filepath.Join(stateDir, "browsers"),
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
	// Podman appliance: fixed /run/dadi (tmpfiles.d). Compose/dev: under state.
	if h.runtime == "podman" {
		h.runDir = defaultRunDir
	} else {
		h.runDir = filepath.Join(h.stateDir, "run")
	}
	if err := os.MkdirAll(h.runDir, 0o755); err != nil {
		return fmt.Errorf("mkdir run dir %s: %w", h.runDir, err)
	}
	h.tmuxSocket = filepath.Join(h.runDir, tmuxSocketName)
	// Darwin AF_UNIX path limit (~104); keep the socket short when state paths are deep (tests).
	if runtime.GOOS == "darwin" && len(h.tmuxSocket) > 100 {
		h.runDir = filepath.Join(os.TempDir(), fmt.Sprintf("dadi-tmux-%d", os.Getpid()))
		if err := os.MkdirAll(h.runDir, 0o755); err != nil {
			return fmt.Errorf("mkdir short run dir: %w", err)
		}
		h.tmuxSocket = filepath.Join(h.runDir, tmuxSocketName)
	}

	if err := os.MkdirAll(h.browsersDir, 0o755); err != nil {
		return fmt.Errorf("mkdir browsers: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(h.browsersDir); err == nil {
		h.browsersDir = resolved
	}
	if err := h.chownDadi(h.browsersDir); err != nil {
		return err
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

// defaultCwd is used when terminal / glob / grep omit cwd (dadi home = state dir).
func (h *hostRuntime) defaultCwd() string {
	return h.stateDir
}

// writeProtectedPrefixes are OS and dadiOS runtime trees agents must not modify
// via the filesystem API. Reads remain allowed.
func (h *hostRuntime) writeProtectedPrefixes() []string {
	prefixes := []string{
		"/usr",
		"/boot",
		"/etc",
		"/lib",
		"/lib64",
		"/bin",
		"/sbin",
		"/root",
		"/var/lib/containers",
	}
	if runtime.GOOS == "darwin" {
		prefixes = append(prefixes, "/System", "/Library")
	}
	for _, rel := range []string{"modules", "cloudflared", "headscale", "browsers", "run"} {
		prefixes = append(prefixes, filepath.Join(h.stateDir, rel))
	}
	return prefixes
}

func pathUnderPrefix(path, prefix string) bool {
	path = filepath.Clean(path)
	prefix = filepath.Clean(prefix)
	if path == prefix {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(path, prefix+sep)
}

func (h *hostRuntime) isWriteProtected(path string) bool {
	path = filepath.Clean(path)
	for _, prefix := range h.writeProtectedPrefixes() {
		if pathUnderPrefix(path, prefix) {
			return true
		}
		if resolved, err := filepath.EvalSymlinks(prefix); err == nil && pathUnderPrefix(path, resolved) {
			return true
		}
	}
	return false
}

// command returns an *exec.Cmd that runs as dadi on the appliance.
func (h *hostRuntime) command(name string, args ...string) *exec.Cmd {
	if h.switchUser {
		full := append([]string{"-u", dadiUsername, "--", name}, args...)
		return exec.Command("runuser", full...)
	}
	return exec.Command(name, args...)
}
