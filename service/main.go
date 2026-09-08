// Nas HTTP service: device provisioning, box status, and log query for Hath.
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
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.MessageKey {
				a.Key = "msg"
			}
			if a.Key == slog.TimeKey {
				a.Key = "time"
			}
			if a.Key == slog.LevelKey {
				if level, ok := a.Value.Any().(slog.Level); ok {
					return slog.String("level", strings.ToLower(level.String()))
				}
			}
			return a
		},
	}))
	logger = logger.With("service", "nas")
	slog.SetDefault(logger)

	controlURL := os.Getenv("CONTROL_URL")
	if controlURL == "" {
		slog.Error("CONTROL_URL is required")
		os.Exit(1)
	}
	userName := os.Getenv("HEADSCALE_USER")
	if userName == "" {
		slog.Error("HEADSCALE_USER is required")
		os.Exit(1)
	}
	lokiURL := os.Getenv("LOKI_URL")
	if lokiURL == "" {
		slog.Error("LOKI_URL is required")
		os.Exit(1)
	}
	listen := os.Getenv("LISTEN_ADDR")
	if listen == "" {
		slog.Error("LISTEN_ADDR is required")
		os.Exit(1)
	}
	state, err := loadStateConfig()
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}

	started := time.Now()
	startMetricsSampler()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /provision", func(w http.ResponseWriter, r *http.Request) {
		handleProvision(w, r, controlURL, userName)
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		handleStatus(w, r, started, state.runtime)
	})
	mux.HandleFunc("GET /logs", func(w http.ResponseWriter, r *http.Request) {
		handleLogs(w, r, lokiURL)
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	registerConfigRoutes(mux, state)

	slog.Info("nas listening", "addr", listen, "state_dir", state.dir, "runtime", state.runtime)
	if err := http.ListenAndServe(listen, mux); err != nil {
		slog.Error("listen failed", "err", err)
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
	FreeBytes  uint64 `json:"free_bytes"`
	TotalBytes uint64 `json:"total_bytes"`
}

func healthTargets(runtime string) []struct {
	name string
	url  string
} {
	// Prod: host Caddy publishes app containers on localhost.
	// Dev compose: container DNS on the dadi network.
	if runtime == "podman" {
		return []struct {
			name string
			url  string
		}{
			{name: "yaad", url: "http://127.0.0.1:8082/health"},
			{name: "dimaag", url: "http://127.0.0.1:8083/health"},
			{name: "dwar", url: "http://127.0.0.1:8081/health"},
		}
	}
	return []struct {
		name string
		url  string
	}{
		{name: "yaad", url: "http://yaad:8080/health"},
		{name: "dimaag", url: "http://dimaag:8080/health"},
		{name: "dwar", url: "http://dwar:8080/health"},
	}
}

func handleStatus(w http.ResponseWriter, _ *http.Request, started time.Time, runtime string) {
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

	disk := diskStatus{}
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err == nil {
		disk.TotalBytes = st.Blocks * uint64(st.Bsize)
		disk.FreeBytes = st.Bavail * uint64(st.Bsize)
	}

	m := currentMetrics()
	// errors stays empty in the status payload — searchable logs live at GET /logs.
	writeJSON(w, http.StatusOK, statusResponse{
		UptimeSeconds: int64(time.Since(started).Seconds()),
		Services:      services,
		Disk:          disk,
		CPU:           m.CPU,
		Memory:        m.Memory,
		GPU:           m.GPU,
		Errors:        []string{},
	})
}

type logEntry struct {
	Time    string `json:"time"`
	Service string `json:"service"`
	Level   string `json:"level"`
	Msg     string `json:"msg"`
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
			http.Error(w, "limit must be an integer from 1 to 1000", http.StatusBadRequest)
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
			http.Error(w, "from must be RFC3339", http.StatusBadRequest)
			return
		}
		start = t
	}
	if raw := strings.TrimSpace(q.Get("to")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			http.Error(w, "to must be RFC3339", http.StatusBadRequest)
			return
		}
		end = t
	}
	if !end.After(start) {
		http.Error(w, "to must be after from", http.StatusBadRequest)
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
				http.Error(w, "services must be plain names", http.StatusBadRequest)
				return
			}
			escaped = append(escaped, p)
		}
		if len(escaped) == 0 {
			http.Error(w, "services must list at least one name", http.StatusBadRequest)
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
			http.Error(w, "level must be debug, info, warn, or error", http.StatusBadRequest)
			return
		}
	}

	endpoint, err := url.Parse(strings.TrimRight(lokiURL, "/") + "/loki/api/v1/query_range")
	if err != nil {
		http.Error(w, "invalid LOKI_URL", http.StatusInternalServerError)
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
		slog.Error("loki query failed", "err", err)
		http.Error(w, "loki query failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "read loki response", http.StatusBadGateway)
		return
	}
	if resp.StatusCode != http.StatusOK {
		slog.Error("loki returned error", "status", resp.StatusCode, "body", string(body))
		http.Error(w, "loki query failed", http.StatusBadGateway)
		return
	}

	entries, err := parseLokiRange(body, limit)
	if err != nil {
		slog.Error("parse loki response", "err", err)
		http.Error(w, "parse loki response", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, logsResponse{Entries: entries})
}

type lokiRangeResponse struct {
	Data struct {
		Result []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// jsonLogServices are modules that emit JSON lines on stdout. Non-JSON fragments
// (npm banners, Node warnings, postgres-js NOTICE dumps) are noise — skip them
// even if they were already ingested before Alloy started dropping them.
var jsonLogServices = map[string]struct{}{
	"dimaag": {},
	"yaad":   {},
	"dwar":   {},
	"nas":    {},
	"hath":   {},
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
				if t, ok := fields["time"].(string); ok && t != "" {
					entry.Time = t
				} else if n, ok := fields["time"].(float64); ok {
					entry.Time = time.UnixMilli(int64(n)).UTC().Format(time.RFC3339Nano)
				}
			}
			out = append(out, entry)
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
