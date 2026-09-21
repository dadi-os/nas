package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(newLogger())

	userName, err := requireEnv("HEADSCALE_USER")
	if err != nil {
		slog.Error(err.Error(), "code", CodeConfigMissing)
		os.Exit(1)
	}
	lokiURL, err := requireEnv("LOKI_URL")
	if err != nil {
		slog.Error(err.Error(), "code", CodeConfigMissing)
		os.Exit(1)
	}
	listen, err := requireEnv("LISTEN_ADDR")
	if err != nil {
		slog.Error(err.Error(), "code", CodeConfigMissing)
		os.Exit(1)
	}
	state, err := loadStateConfig()
	if err != nil {
		slog.Error(err.Error(), "code", CodeConfigMissing)
		os.Exit(1)
	}
	host, err := newHostRuntime(state)
	if err != nil {
		slog.Error(err.Error(), "code", CodeConfigMissing)
		os.Exit(1)
	}
	if err := state.ensureChaaviTLS(); err != nil {
		slog.Error(err.Error(), "code", CodeInternal)
		os.Exit(1)
	}
	terminals := newTerminalHost(host)
	files := newFSHost(host)
	browsers := newBrowserHost(host)

	started := time.Now()
	startMetricsSampler()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ca", func(w http.ResponseWriter, r *http.Request) {
		handleMeshCA(w, r, state)
	})
	mux.HandleFunc("POST /provision", func(w http.ResponseWriter, r *http.Request) {
		controlURL, err := state.mintControlURL()
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeConfigMissing, err.Error())
			return
		}
		handleProvision(w, r, controlURL, userName, state)
	})
	mux.HandleFunc("GET /clients", func(w http.ResponseWriter, r *http.Request) {
		handleListClients(w, r, userName, state.dir)
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		handleStatus(w, r, started, state.runtime, state.dir)
	})
	mux.HandleFunc("GET /logs", func(w http.ResponseWriter, r *http.Request) {
		handleLogs(w, r, lokiURL)
	})
	mux.HandleFunc("GET /logs/services", func(w http.ResponseWriter, r *http.Request) {
		handleLogServices(w, r, lokiURL, state.runtime)
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	registerConfigRoutes(mux, state)
	registerDwarSettingsRoutes(mux, state)
	registerAccessRoutes(mux, state)
	terminals.register(mux)
	files.register(mux)
	browsers.register(mux)

	slog.Info("nas listening", "addr", listen, "state_dir", state.dir, "runtime", state.runtime)
	if err := http.ListenAndServe(listen, withRequestLog(mux)); err != nil {
		slog.Error("listen failed", "code", CodeInternal, "err", err)
		os.Exit(1)
	}
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
	// CaPem is the mesh CA certificate (PEM). Hath installs it so https://chaavi.dadi works.
	CaPem string `json:"ca_pem,omitempty"`
}

func handleProvision(w http.ResponseWriter, r *http.Request, controlURL, userName string, state stateConfig) {
	var req provisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
		return
	}
	nodeName := strings.TrimSpace(req.NodeName)
	if nodeName == "" {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "node_name is required")
		return
	}

	userID, err := resolveUserID(userName)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeProvisionFailed, err.Error())
		return
	}

	provisionMu.Lock()
	defer provisionMu.Unlock()

	now := time.Now()
	clients, err := listMeshClients(userName)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeProvisionFailed, err.Error())
		return
	}
	pending, err := loadPendingNodes(state.dir, now)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	pruneJoinedPending(pending, clients)
	if err := savePendingNodes(state.dir, pending, now); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	if nodeNameTaken(nodeName, takenNodeNames(clients, pending, now)) {
		writeError(w, r, http.StatusConflict, CodeConflict, "a device named "+nodeName+" already exists")
		return
	}

	pendingKey := strings.ToLower(nodeName)
	pending[pendingKey] = now.Add(pendingNodeTTL)
	if err := savePendingNodes(state.dir, pending, now); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}

	authKey, err := mintDeviceKey(userID)
	if err != nil {
		rollbackPendingNode(state.dir, pending, pendingKey)
		writeError(w, r, http.StatusInternalServerError, CodeProvisionFailed, err.Error())
		return
	}

	caPem, err := state.readChaaviCAPem()
	if err != nil {
		rollbackPendingNode(state.dir, pending, pendingKey)
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	if caPem == "" {
		rollbackPendingNode(state.dir, pending, pendingKey)
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "mesh CA not generated")
		return
	}

	payload, err := json.Marshal(credentialsBundle{
		ControlURL: controlURL,
		AuthKey:    authKey,
		NodeName:   nodeName,
		CaPem:      caPem,
	})
	if err != nil {
		rollbackPendingNode(state.dir, pending, pendingKey)
		writeError(w, r, http.StatusInternalServerError, CodeProvisionFailed, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, provisionResponse{
		Bundle: base64.StdEncoding.EncodeToString(payload),
	})
}

// handleMeshCA serves the mesh CA PEM for Hath trust install (and re-join without re-provision).
func handleMeshCA(w http.ResponseWriter, r *http.Request, state stateConfig) {
	pem, err := state.readChaaviCAPem()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	if pem == "" {
		writeError(w, r, http.StatusNotFound, CodeNotFound, "mesh CA not generated")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, pem)
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

type meshClient struct {
	NodeName    string   `json:"node_name"`
	Online      bool     `json:"online"`
	Pending     bool     `json:"pending"`
	LastSeen    *string  `json:"last_seen"`
	IPAddresses []string `json:"ip_addresses"`
}

type headscaleNode struct {
	Name        string   `json:"name"`
	GivenName   string   `json:"givenName"`
	Online      bool     `json:"online"`
	LastSeen    *string  `json:"lastSeen"`
	IPAddresses []string `json:"ipAddresses"`
}

// handleListClients returns Headscale mesh nodes plus unexpired pending setup names.
func handleListClients(w http.ResponseWriter, r *http.Request, userName, stateDir string) {
	provisionMu.Lock()
	defer provisionMu.Unlock()

	clients, err := listMeshClients(userName)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	now := time.Now()
	pending, err := loadPendingNodes(stateDir, now)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	pruneJoinedPending(pending, clients)
	if err := savePendingNodes(stateDir, pending, now); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": withPendingClients(clients, pending, now)})
}

// listMeshClients runs `headscale nodes list -o json` and maps nodes to meshClient values.
func listMeshClients(userName string) ([]meshClient, error) {
	out, err := exec.Command("headscale", "nodes", "list", "--user", userName, "-o", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	nodes, err := parseHeadscaleNodes(out)
	if err != nil {
		return nil, err
	}
	clients := make([]meshClient, 0, len(nodes))
	for _, node := range nodes {
		name := strings.TrimSpace(node.GivenName)
		if name == "" {
			name = strings.TrimSpace(node.Name)
		}
		if name == "" {
			continue
		}
		ips := node.IPAddresses
		if ips == nil {
			ips = []string{}
		}
		var lastSeen *string
		if node.LastSeen != nil {
			trimmed := strings.TrimSpace(*node.LastSeen)
			if trimmed != "" {
				lastSeen = &trimmed
			}
		}
		clients = append(clients, meshClient{
			NodeName:    name,
			Online:      node.Online,
			LastSeen:    lastSeen,
			IPAddresses: ips,
		})
	}
	return clients, nil
}

// parseHeadscaleNodes decodes a JSON array from `headscale nodes list -o json`.
func parseHeadscaleNodes(out []byte) ([]headscaleNode, error) {
	var nodes []headscaleNode
	if err := json.Unmarshal(out, &nodes); err != nil {
		return nil, fmt.Errorf("parse nodes: %w", err)
	}
	return nodes, nil
}

type statusResponse struct {
	UptimeSeconds int64           `json:"uptime_seconds"`
	Services      []serviceStatus `json:"services"`
	Disk          diskStatus      `json:"disk"`
	CPU           *cpuStatus      `json:"cpu,omitempty"`
	Memory        *memoryStatus   `json:"memory,omitempty"`
	GPU           []gpuStatus     `json:"gpu,omitempty"`
	Errors        []string        `json:"errors"`
}

type serviceStatus struct {
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
}

type diskStatus struct {
	FreeBytes   uint64  `json:"free_bytes"`
	TotalBytes  uint64  `json:"total_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

// diskStatusFromStatfs builds a disk meter from block counts. free may exceed
// total on some filesystems; used is then 0.
func diskStatusFromStatfs(total, free uint64) diskStatus {
	used := uint64(0)
	if total > free {
		used = total - free
	}
	pct := 0.0
	if total > 0 {
		pct = float64(used) / float64(total) * 100
	}
	return diskStatus{FreeBytes: free, TotalBytes: total, UsedPercent: pct}
}

// readDisk reports usage of the filesystem that holds path. Ostree's `/` is a
// packed composefs with no free space; DADI_STATE_DIR lives on the writable volume.
func readDisk(path string) (diskStatus, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return diskStatus{}, fmt.Errorf("statfs %s: %w", path, err)
	}
	return diskStatusFromStatfs(st.Blocks*uint64(st.Bsize), st.Bavail*uint64(st.Bsize)), nil
}

func healthTargets(runtime string) []struct {
	name string
	url  string
} {
	if runtime == "podman" {
		return []struct {
			name string
			url  string
		}{
			{name: "yaad", url: "http://127.0.0.1:8082/health"},
			{name: "dimaag", url: "http://127.0.0.1:8083/health"},
			{name: "dwar", url: "http://127.0.0.1:8081/health"},
			{name: "ghar", url: "http://127.0.0.1:8084/health"},
			{name: "chaavi", url: "http://127.0.0.1:8085/health"},
		}
	}
	return []struct {
		name string
		url  string
	}{
		{name: "yaad", url: "http://yaad:8080/health"},
		{name: "dimaag", url: "http://dimaag:8080/health"},
		{name: "dwar", url: "http://dwar:8080/health"},
		{name: "ghar", url: "http://ghar:8080/health"},
		{name: "chaavi", url: "http://chaavi:8080/health"},
	}
}

func handleStatus(w http.ResponseWriter, _ *http.Request, started time.Time, runtime, stateDir string) {
	targets := healthTargets(runtime)
	services := make([]serviceStatus, 0, len(targets))
	client := &http.Client{Timeout: 2 * time.Second}
	for _, t := range targets {
		resp, err := client.Get(t.url)
		healthy := false
		if err == nil {
			healthy = resp.StatusCode == http.StatusOK
			resp.Body.Close()
		}
		services = append(services, serviceStatus{Name: t.name, Healthy: healthy})
	}
	services = append(services, serviceStatus{Name: "nas", Healthy: true})

	disk, diskErr := readDisk(stateDir)
	errs := []string{}
	if diskErr != nil {
		errs = append(errs, diskErr.Error())
	}

	m := currentMetrics()
	writeJSON(w, http.StatusOK, statusResponse{
		UptimeSeconds: int64(time.Since(started).Seconds()),
		Services:      services,
		Disk:          disk,
		CPU:           m.CPU,
		Memory:        m.Memory,
		GPU:           m.GPU,
		Errors:        errs,
	})
}

type logEntry struct {
	Time    string `json:"time"`
	Service string `json:"service"`
	Level   string `json:"level"`
	Msg     string `json:"msg"`
	Code    string `json:"code,omitempty"`
	Raw     string `json:"raw,omitempty"`
}

type logsResponse struct {
	Entries []logEntry `json:"entries"`
}

func handleLogs(w http.ResponseWriter, r *http.Request, lokiURL string) {
	q := r.URL.Query()
	services := strings.TrimSpace(q.Get("services"))
	level := strings.ToLower(strings.TrimSpace(q.Get("level")))
	text := strings.TrimSpace(q.Get("q"))
	limit := 100
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 1000 {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "limit must be an integer from 1 to 1000")
			return
		}
		limit = n
	}

	now := time.Now().UTC()
	end := now
	start := now.Add(-1 * time.Hour)
	if raw := strings.TrimSpace(q.Get("from")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "from must be RFC3339")
			return
		}
		start = t
	}
	if raw := strings.TrimSpace(q.Get("to")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "to must be RFC3339")
			return
		}
		end = t
	}
	if !end.After(start) {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "to must be after from")
		return
	}

	selector := `{service=~".+"}`
	if services != "" {
		parts := strings.Split(services, ",")
		escaped := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if strings.ContainsAny(p, `.|+*?[](){}\`) {
				writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "services must be plain names")
				return
			}
			escaped = append(escaped, p)
		}
		if len(escaped) == 0 {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "services must list at least one name")
			return
		}
		selector = fmt.Sprintf(`{service=~"%s"}`, strings.Join(escaped, "|"))
	}

	logql := selector
	if text != "" {
		logql += fmt.Sprintf(` |= %q`, text)
	}
	logql += ` | json`
	if level != "" {
		switch level {
		case "debug", "info", "warn", "error":
			logql += fmt.Sprintf(` | level="%s"`, level)
		default:
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "level must be debug, info, warn, or error")
			return
		}
	}

	endpoint, err := url.Parse(strings.TrimRight(lokiURL, "/") + "/loki/api/v1/query_range")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeLogQueryFailed, "invalid LOKI_URL")
		return
	}
	params := endpoint.Query()
	params.Set("query", logql)
	params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	params.Set("limit", strconv.Itoa(limit))
	params.Set("direction", "backward")
	endpoint.RawQuery = params.Encode()

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(endpoint.String())
	if err != nil {
		slog.Error("loki query failed", "code", CodeLogQueryFailed, "err", err)
		writeError(w, r, http.StatusBadGateway, CodeLogQueryFailed, "loki query failed")
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, CodeLogQueryFailed, "read loki response")
		return
	}
	if resp.StatusCode != http.StatusOK {
		slog.Error("loki returned error", "code", CodeLogQueryFailed, "status", resp.StatusCode, "body", string(body))
		writeError(w, r, http.StatusBadGateway, CodeLogQueryFailed, "loki query failed")
		return
	}

	entries, err := parseLokiRange(body, limit)
	if err != nil {
		slog.Error("parse loki response", "code", CodeLogQueryFailed, "err", err)
		writeError(w, r, http.StatusBadGateway, CodeLogQueryFailed, "parse loki response")
		return
	}
	writeJSON(w, http.StatusOK, logsResponse{Entries: entries})
}

type lokiLabelValues struct {
	Data []string `json:"data"`
}

type logServicesResponse struct {
	Services []string `json:"services"`
}

// handleLogServices is GET /logs/services — Loki `service` label values unioned
// with the modules Nas health-checks, so chips exist before a service has logs.
func handleLogServices(w http.ResponseWriter, r *http.Request, lokiURL, runtime string) {
	q := r.URL.Query()
	now := time.Now().UTC()
	end := now
	start := now.Add(-24 * time.Hour)
	if raw := strings.TrimSpace(q.Get("from")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "from must be RFC3339")
			return
		}
		start = t
	}
	if raw := strings.TrimSpace(q.Get("to")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "to must be RFC3339")
			return
		}
		end = t
	}
	if !end.After(start) {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "to must be after from")
		return
	}

	endpoint, err := url.Parse(strings.TrimRight(lokiURL, "/") + "/loki/api/v1/label/service/values")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeLogQueryFailed, "invalid LOKI_URL")
		return
	}
	params := endpoint.Query()
	params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	endpoint.RawQuery = params.Encode()

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(endpoint.String())
	if err != nil {
		slog.Error("loki label query failed", "code", CodeLogQueryFailed, "err", err)
		writeError(w, r, http.StatusBadGateway, CodeLogQueryFailed, "loki query failed")
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, CodeLogQueryFailed, "read loki response")
		return
	}
	if resp.StatusCode != http.StatusOK {
		slog.Error("loki returned error", "code", CodeLogQueryFailed, "status", resp.StatusCode, "body", string(body))
		writeError(w, r, http.StatusBadGateway, CodeLogQueryFailed, "loki query failed")
		return
	}

	fromLoki, err := parseLokiLabelValues(body)
	if err != nil {
		slog.Error("parse loki label values", "code", CodeLogQueryFailed, "err", err)
		writeError(w, r, http.StatusBadGateway, CodeLogQueryFailed, "parse loki response")
		return
	}
	writeJSON(w, http.StatusOK, logServicesResponse{
		Services: mergeLogServices(knownLogServices(runtime), fromLoki),
	})
}

// knownLogServices is the module names Nas health-checks, plus nas itself.
func knownLogServices(runtime string) []string {
	targets := healthTargets(runtime)
	names := make([]string, 0, len(targets)+1)
	for _, t := range targets {
		names = append(names, t.name)
	}
	names = append(names, "nas")
	return names
}

// mergeLogServices unions known module names with Loki label values, sorted.
func mergeLogServices(known, fromLoki []string) []string {
	set := make(map[string]struct{}, len(known)+len(fromLoki))
	for _, n := range known {
		n = strings.TrimSpace(n)
		if n != "" {
			set[n] = struct{}{}
		}
	}
	for _, n := range fromLoki {
		n = strings.TrimSpace(n)
		if n != "" {
			set[n] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// parseLokiLabelValues decodes GET /loki/api/v1/label/service/values.
func parseLokiLabelValues(body []byte) ([]string, error) {
	var parsed lokiLabelValues
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	return parsed.Data, nil
}

type lokiRangeResponse struct {
	Data struct {
		Result []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

var jsonLogServices = map[string]struct{}{
	"dimaag": {},
	"yaad":   {},
	"dwar":   {},
	"nas":    {},
	"hath":   {},
	"ghar":   {},
	"chaavi": {},
}

func parseLokiRange(body []byte, limit int) ([]logEntry, error) {
	var parsed lokiRangeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	out := make([]logEntry, 0, limit)
	for _, series := range parsed.Data.Result {
		service := series.Stream["service"]
		_, requireJSON := jsonLogServices[service]
		for _, pair := range series.Values {
			if len(pair) < 2 {
				continue
			}
			ns, err := strconv.ParseInt(pair[0], 10, 64)
			if err != nil {
				continue
			}
			raw := strings.TrimSpace(pair[1])
			if raw == "" {
				continue
			}
			entry := logEntry{
				Time:    time.Unix(0, ns).UTC().Format(time.RFC3339Nano),
				Service: service,
				Level:   "info",
				Msg:     raw,
				Raw:     raw,
			}
			var fields map[string]any
			if err := json.Unmarshal([]byte(raw), &fields); err != nil {
				if requireJSON {
					continue
				}
			} else {
				if msg, ok := fields["msg"].(string); ok {
					entry.Msg = msg
				} else if message, ok := fields["message"].(string); ok {
					entry.Msg = message
				} else if requireJSON {
					continue
				}
				if lvl, ok := fields["level"].(string); ok {
					entry.Level = strings.ToLower(lvl)
				}
				if svc, ok := fields["service"].(string); ok && svc != "" {
					entry.Service = svc
				}
				if code, ok := fields["code"].(string); ok && code != "" {
					entry.Code = code
				}
				if t, ok := fields["time"].(string); ok && t != "" {
					entry.Time = t
				} else if n, ok := fields["time"].(float64); ok {
					entry.Time = time.UnixMilli(int64(n)).UTC().Format(time.RFC3339Nano)
				}
			}
			out = append(out, entry)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return logEntryTime(out[i]).After(logEntryTime(out[j]))
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// logEntryTime parses an entry's time for newest-first ordering; zero if unparseable.
func logEntryTime(entry logEntry) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, entry.Time); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, entry.Time); err == nil {
		return t
	}
	return time.Time{}
}
