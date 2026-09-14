package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"
)

const (
	sshAdminUser   = "ankur"
	sessionUser    = "dadi"
	minSSHPassword = 8
	maxSSHPassword = 128
	tpmStatusPath  = "/var/lib/dadi/tpm.json"
)

type accessStatus struct {
	SSHUser     string    `json:"ssh_user"`
	SessionUser string    `json:"session_user"`
	PasswordSet bool      `json:"password_set"`
	TPM         tpmStatus `json:"tpm"`
}

type tpmStatus struct {
	Present  bool   `json:"present"`
	Enrolled bool   `json:"enrolled"`
	PCRs     string `json:"pcrs,omitempty"`
	Device   string `json:"device,omitempty"`
}

type sshPasswordRequest struct {
	Password string `json:"password"`
}

// registerAccessRoutes exposes appliance SSH password and TPM enroll status.
func registerAccessRoutes(mux *http.ServeMux, s stateConfig) {
	mux.HandleFunc("GET /access", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, accessStatus{
			SSHUser:     sshAdminUser,
			SessionUser: sessionUser,
			PasswordSet: sshPasswordIsSet(),
			TPM:         readTPMStatus(),
		})
	})

	mux.HandleFunc("PUT /access/ssh-password", func(w http.ResponseWriter, r *http.Request) {
		if s.runtime != "podman" {
			writeError(w, r, http.StatusForbidden, CodeForbidden, "SSH password is appliance-only")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "read body")
			return
		}
		var req sshPasswordRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
			return
		}
		if err := validateSSHPassword(req.Password); err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, err.Error())
			return
		}
		if err := setSSHPassword(req.Password); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

// validateSSHPassword rejects passwords outside 8–128 runes or containing newlines.
func validateSSHPassword(password string) error {
	n := utf8.RuneCountInString(password)
	if n < minSSHPassword {
		return fmt.Errorf("password must be at least %d characters", minSSHPassword)
	}
	if n > maxSSHPassword {
		return fmt.Errorf("password must be at most %d characters", maxSSHPassword)
	}
	if strings.ContainsAny(password, "\n\r") {
		return fmt.Errorf("password must not contain newlines")
	}
	return nil
}

func sshPasswordIsSet() bool {
	out, err := exec.Command("passwd", "-S", sshAdminUser).Output()
	if err != nil {
		return false
	}
	return passwordSetFromPasswdS(string(out))
}

func passwordSetFromPasswdS(out string) bool {
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return false
	}
	return fields[1] == "P"
}

func setSSHPassword(password string) error {
	cmd := exec.Command("passwd", "--stdin", sshAdminUser)
	cmd.Stdin = strings.NewReader(password + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("passwd: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func readTPMStatus() tpmStatus {
	body, err := os.ReadFile(tpmStatusPath)
	if err != nil {
		return tpmStatus{
			Present: fileExists("/dev/tpmrm0") || fileExists("/dev/tpm0"),
		}
	}
	var st tpmStatus
	if err := json.Unmarshal(body, &st); err != nil {
		return tpmStatus{
			Present: fileExists("/dev/tpmrm0") || fileExists("/dev/tpm0"),
		}
	}
	return st
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
