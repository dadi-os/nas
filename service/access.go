package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	sessionUser    = "dadi"
	minSSHPassword = 8
	maxSSHPassword = 128
	minSSHUID      = 1000
	nobodyUID      = 65534
	tpmStatusPath  = "/var/lib/dadi/tpm.json"
	sshAllowConfig = "/etc/ssh/sshd_config.d/20-dadi-allow.conf"
)

var (
	sshUsernameRE    = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)
	reservedSSHUsers = map[string]struct{}{
		"dadi": {}, "root": {}, "nobody": {}, "nfsnobody": {},
		"daemon": {}, "bin": {}, "sys": {}, "sync": {}, "mail": {},
		"ftp": {}, "sshd": {}, "halt": {}, "shutdown": {}, "setup": {},
	}
	hiddenSSHUsers = map[string]struct{}{
		"setup": {},
	}
)

type accessStatus struct {
	SessionUser string       `json:"session_user"`
	Users       []accessUser `json:"users"`
	TPM         tpmStatus    `json:"tpm"`
}

type accessUser struct {
	Name        string `json:"name"`
	PasswordSet bool   `json:"password_set"`
}

type tpmStatus struct {
	Present  bool   `json:"present"`
	Enrolled bool   `json:"enrolled"`
	PCRs     string `json:"pcrs,omitempty"`
	Device   string `json:"device,omitempty"`
}

type sshUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// registerAccessRoutes exposes appliance SSH user management and TPM status.
func registerAccessRoutes(mux *http.ServeMux, s stateConfig) {
	mux.HandleFunc("GET /access", func(w http.ResponseWriter, r *http.Request) {
		users := []accessUser{}
		if s.runtime == "podman" {
			var err error
			users, err = listSSHUsers()
			if err != nil {
				writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, accessStatus{
			SessionUser: sessionUser,
			Users:       users,
			TPM:         readTPMStatus(),
		})
	})

	mux.HandleFunc("POST /access/users", func(w http.ResponseWriter, r *http.Request) {
		if s.runtime != "podman" {
			writeError(w, r, http.StatusForbidden, CodeForbidden, "SSH users are appliance-only")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "read body")
			return
		}
		var req sshUserRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
			return
		}
		created, err := provisionSSHUser(req.Username, req.Password)
		if err != nil {
			if isValidateErr(err) {
				writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, err.Error())
				return
			}
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":   "ok",
			"username": strings.TrimSpace(req.Username),
			"created":  created,
		})
	})

	mux.HandleFunc("DELETE /access/users/{username}", func(w http.ResponseWriter, r *http.Request) {
		if s.runtime != "podman" {
			writeError(w, r, http.StatusForbidden, CodeForbidden, "SSH users are appliance-only")
			return
		}
		username := r.PathValue("username")
		if err := removeSSHUser(username); err != nil {
			if errors.Is(err, errSSHNotFound) {
				writeError(w, r, http.StatusNotFound, CodeNotFound, err.Error())
				return
			}
			if isValidateErr(err) {
				writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, err.Error())
				return
			}
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":   "ok",
			"username": strings.TrimSpace(username),
		})
	})
}

var (
	errSSHReserved = errors.New("username is reserved")
	errSSHNotFound = errors.New("user not found")
)

// isValidateErr reports whether err is a client-facing username or password rejection.
func isValidateErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.HasPrefix(msg, "username ") ||
		strings.HasPrefix(msg, "password ") ||
		errors.Is(err, errSSHReserved)
}

// validateSSHUsername rejects names that are not a Linux login we may create.
func validateSSHUsername(username string) error {
	if !sshUsernameRE.MatchString(username) {
		return fmt.Errorf("username must be 2–32 characters, start with a letter, and use only lowercase letters, digits, _ or -")
	}
	if _, ok := reservedSSHUsers[username]; ok {
		return errSSHReserved
	}
	if strings.HasPrefix(username, "systemd") {
		return errSSHReserved
	}
	return nil
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

type passwdRecord struct {
	name string
	uid  int
}

// parsePasswdLine reads one getent passwd line. ok is false when the line is not a user record.
func parsePasswdLine(line string) (passwdRecord, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return passwdRecord{}, false
	}
	fields := strings.Split(line, ":")
	if len(fields) < 3 {
		return passwdRecord{}, false
	}
	uid, err := strconv.Atoi(fields[2])
	if err != nil {
		return passwdRecord{}, false
	}
	return passwdRecord{name: fields[0], uid: uid}, true
}

// isSSHAdminUID reports whether uid is a human login we may expose over SSH.
func isSSHAdminUID(uid int) bool {
	return uid >= minSSHUID && uid < nobodyUID
}

// sshAdminNamesFromPasswd returns human login names from a getent passwd dump, excluding dadi.
func sshAdminNamesFromPasswd(out string) []string {
	names := make([]string, 0)
	for _, line := range strings.Split(out, "\n") {
		rec, ok := parsePasswdLine(line)
		if !ok {
			continue
		}
		if rec.name == sessionUser {
			continue
		}
		if _, hidden := hiddenSSHUsers[rec.name]; hidden {
			continue
		}
		if !isSSHAdminUID(rec.uid) {
			continue
		}
		names = append(names, rec.name)
	}
	sort.Strings(names)
	return names
}

// listSSHUsers returns human SSH logins on the appliance, excluding dadi.
func listSSHUsers() ([]accessUser, error) {
	out, err := exec.Command("getent", "passwd").Output()
	if err != nil {
		return nil, fmt.Errorf("getent passwd: %w", err)
	}
	names := sshAdminNamesFromPasswd(string(out))
	users := make([]accessUser, 0, len(names))
	for _, name := range names {
		status, err := exec.Command("passwd", "-S", name).Output()
		if err != nil {
			return nil, fmt.Errorf("passwd -S %s: %w", name, err)
		}
		users = append(users, accessUser{
			Name:        name,
			PasswordSet: passwordSetFromPasswdS(string(status)),
		})
	}
	return users, nil
}

// passwordSetFromPasswdS is true when passwd -S reports a usable password (P).
func passwordSetFromPasswdS(out string) bool {
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return false
	}
	return fields[1] == "P"
}

// lookupPasswd returns the passwd record for name. exists is false when getent exits 2.
func lookupPasswd(name string) (passwdRecord, bool, error) {
	out, err := exec.Command("getent", "passwd", name).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 2 {
			return passwdRecord{}, false, nil
		}
		return passwdRecord{}, false, fmt.Errorf("getent passwd %s: %w", name, err)
	}
	rec, ok := parsePasswdLine(strings.TrimSpace(string(out)))
	if !ok {
		return passwdRecord{}, false, fmt.Errorf("getent passwd %s: unreadable record", name)
	}
	return rec, true, nil
}

// provisionSSHUser creates a wheel SSH user or sets the password on an existing one.
func provisionSSHUser(username, password string) (created bool, err error) {
	username = strings.TrimSpace(username)
	if err := validateSSHUsername(username); err != nil {
		return false, err
	}
	if err := validateSSHPassword(password); err != nil {
		return false, err
	}
	rec, exists, err := lookupPasswd(username)
	if err != nil {
		return false, err
	}
	if exists {
		if rec.name == sessionUser || !isSSHAdminUID(rec.uid) {
			return false, errSSHReserved
		}
	} else {
		cmd := exec.Command("useradd", "--create-home", "--groups", "wheel", "--shell", "/bin/bash", username)
		if out, err := cmd.CombinedOutput(); err != nil {
			return false, fmt.Errorf("useradd: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		created = true
	}
	if err := setSSHPassword(username, password); err != nil {
		return created, err
	}
	if err := syncSSHAllowUsers(); err != nil {
		return created, err
	}
	return created, nil
}

// removeSSHUser deletes an appliance SSH login and rewrites AllowUsers.
func removeSSHUser(username string) error {
	username = strings.TrimSpace(username)
	if err := validateSSHUsername(username); err != nil {
		return err
	}
	rec, exists, err := lookupPasswd(username)
	if err != nil {
		return err
	}
	if !exists {
		return errSSHNotFound
	}
	if rec.name == sessionUser || !isSSHAdminUID(rec.uid) {
		return errSSHReserved
	}
	cmd := exec.Command("userdel", "--remove", username)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("userdel: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return syncSSHAllowUsers()
}

// setSSHPassword sets username's password via passwd --stdin.
func setSSHPassword(username, password string) error {
	cmd := exec.Command("passwd", "--stdin", username)
	cmd.Stdin = strings.NewReader(password + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("passwd: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// syncSSHAllowUsers rewrites sshd AllowUsers from current SSH admins and reloads sshd.
func syncSSHAllowUsers() error {
	users, err := listSSHUsers()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(users))
	for _, u := range users {
		names = append(names, u.Name)
	}
	if err := writeAllowUsers(names); err != nil {
		return err
	}
	return reloadSSHD()
}

// allowUsersFileBody is the sshd snippet that restricts SSH to provisioned logins.
func allowUsersFileBody(names []string) string {
	sort.Strings(names)
	return "AllowUsers " + strings.Join(names, " ") + "\n"
}

// writeAllowUsers writes or removes the generated sshd AllowUsers drop-in.
func writeAllowUsers(names []string) error {
	if len(names) == 0 {
		if err := os.Remove(sshAllowConfig); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove ssh allow: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(sshAllowConfig, []byte(allowUsersFileBody(names)), 0644); err != nil {
		return fmt.Errorf("write ssh allow: %w", err)
	}
	return nil
}

// reloadSSHD validates sshd config then reloads the unit.
func reloadSSHD() error {
	if out, err := exec.Command("sshd", "-t").CombinedOutput(); err != nil {
		return fmt.Errorf("sshd -t: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("systemctl", "reload", "sshd.service").CombinedOutput(); err != nil {
		return fmt.Errorf("reload sshd: %w (%s)", err, strings.TrimSpace(string(out)))
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
