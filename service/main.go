// Nas HTTP service: device provisioning and box status for Hath.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	controlURL := os.Getenv("CONTROL_URL")
	if controlURL == "" {
		log.Fatal("CONTROL_URL is required")
	}
	userName := os.Getenv("HEADSCALE_USER")
	if userName == "" {
		log.Fatal("HEADSCALE_USER is required")
	}
	listen := os.Getenv("LISTEN_ADDR")
	if listen == "" {
		listen = ":8080"
	}

	started := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /provision", func(w http.ResponseWriter, r *http.Request) {
		handleProvision(w, r, controlURL, userName)
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		handleStatus(w, r, started)
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	log.Printf("nas-service listening on %s", listen)
	log.Fatal(http.ListenAndServe(listen, mux))
}

type provisionRequest struct {
	NodeName string `json:"node_name"`
}

type provisionResponse struct {
	Bundle string `json:"bundle"`
}

type credentialsBundle struct {
	ControlURL string `json:"control_url"`
	AuthKey    string `json:"auth_key"`
	NodeName   string `json:"node_name"`
}

func handleProvision(w http.ResponseWriter, r *http.Request, controlURL, userName string) {
	var req provisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	nodeName := strings.TrimSpace(req.NodeName)
	if nodeName == "" {
		http.Error(w, "node_name is required", http.StatusBadRequest)
		return
	}

	userID, err := resolveUserID(userName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	authKey, err := mintDeviceKey(userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	payload, err := json.Marshal(credentialsBundle{
		ControlURL: controlURL,
		AuthKey:    authKey,
		NodeName:   nodeName,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, provisionResponse{
		Bundle: base64.StdEncoding.EncodeToString(payload),
	})
}

type headscaleUser struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

type headscalePreAuthKey struct {
	Key string `json:"key"`
}

func resolveUserID(name string) (uint64, error) {
	out, err := exec.Command("headscale", "users", "list", "-o", "json").Output()
	if err != nil {
		return 0, fmt.Errorf("list users: %w", err)
	}
	var users []headscaleUser
	if err := json.Unmarshal(out, &users); err != nil {
		return 0, fmt.Errorf("parse users: %w", err)
	}
	for _, u := range users {
		if u.Name == name {
			return u.ID, nil
		}
	}
	return 0, fmt.Errorf("user %q not found", name)
}

func mintDeviceKey(userID uint64) (string, error) {
	// Single-use, short-lived. Do not pass --reusable.
	out, err := exec.Command(
		"headscale", "preauthkeys", "create",
		"--user", strconv.FormatUint(userID, 10),
		"--expiration", "1h",
		"-o", "json",
	).Output()
	if err != nil {
		return "", fmt.Errorf("create preauthkey: %w", err)
	}
	var key headscalePreAuthKey
	if err := json.Unmarshal(out, &key); err != nil {
		return "", fmt.Errorf("parse preauthkey: %w", err)
	}
	if strings.TrimSpace(key.Key) == "" {
		return "", fmt.Errorf("preauthkey response missing key")
	}
	return key.Key, nil
}

type statusResponse struct {
	UptimeSeconds int64           `json:"uptime_seconds"`
	Services      []serviceStatus `json:"services"`
	Disk          diskStatus      `json:"disk"`
	Errors        []string        `json:"errors"`
}

type serviceStatus struct {
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
}

type diskStatus struct {
	FreeBytes  uint64 `json:"free_bytes"`
	TotalBytes uint64 `json:"total_bytes"`
}

var healthTargets = []struct {
	name string
	url  string
}{
	{name: "yaad", url: "http://yaad:8080/health"},
	{name: "dimaag", url: "http://dimaag:8080/health"},
	{name: "dwar", url: "http://dwar:8080/health"},
}

func handleStatus(w http.ResponseWriter, _ *http.Request, started time.Time) {
	services := make([]serviceStatus, 0, len(healthTargets))
	client := &http.Client{Timeout: 2 * time.Second}
	for _, t := range healthTargets {
		resp, err := client.Get(t.url)
		healthy := false
		if err == nil {
			healthy = resp.StatusCode == http.StatusOK
			resp.Body.Close()
		}
		services = append(services, serviceStatus{Name: t.name, Healthy: healthy})
	}

	disk := diskStatus{}
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err == nil {
		disk.TotalBytes = st.Blocks * uint64(st.Bsize)
		disk.FreeBytes = st.Bavail * uint64(st.Bsize)
	}

	// errors stays empty in dev — journald does not exist under Docker.
	writeJSON(w, http.StatusOK, statusResponse{
		UptimeSeconds: int64(time.Since(started).Seconds()),
		Services:      services,
		Disk:          disk,
		Errors:        []string{},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
