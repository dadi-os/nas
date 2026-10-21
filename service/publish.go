package main

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const composeControlURL = "http://localhost:8080"

const etcHeadscaleConfig = "/etc/headscale/config.yaml"

// meshExtraRecordIP is the appliance mesh address extra records point at.
// Headscale assigns 100.64.0.1 to the first node (`os`).
const meshExtraRecordIP = "100.64.0.1"

var (
	serverURLLine     = regexp.MustCompile(`(?m)^server_url:\s*.*$`)
	extraRecordNameRe = regexp.MustCompile(`(?m)^\s+-\s+name:\s+"([^"]+)"`)
	relayHostnameRe   = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)
)

// controlHostname is the hostname devices/Tailscale dial from a control plane URL.
func controlHostname(controlURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(controlURL))
	if err != nil {
		return "", fmt.Errorf("parse control URL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("control URL has no host")
	}
	return host, nil
}

// applyServerURL sets Headscale server_url in a config.yaml body.
func applyServerURL(config []byte, controlURL string) ([]byte, error) {
	if !serverURLLine.Match(config) {
		return nil, fmt.Errorf("headscale config has no server_url line")
	}
	repl := []byte("server_url: " + strings.TrimSpace(controlURL))
	return serverURLLine.ReplaceAll(config, repl), nil
}

// headscaleConfigPath is the writable Headscale config under DADI_STATE_DIR.
func (s stateConfig) headscaleConfigPath() string {
	return filepath.Join(s.dir, "headscale", "config.yaml")
}

// publishStatus is GET /headscale/publish.
type publishStatus struct {
	ControlURL string `json:"control_url"`
	Hostname   string `json:"hostname,omitempty"`
}

// relayHostnamePath is the file holding the public hostname the relay forwards to Headscale.
func (s stateConfig) relayHostnamePath() string {
	return filepath.Join(s.dir, "relay", "hostname")
}

// relayHostname reads and validates the relay's public hostname. The appliance
// has no control URL until the file exists.
func (s stateConfig) relayHostname() (string, error) {
	body, err := os.ReadFile(s.relayHostnamePath())
	if err != nil {
		return "", fmt.Errorf("read relay hostname: %w", err)
	}
	host := strings.TrimSpace(string(body))
	if !relayHostnameRe.MatchString(host) {
		return "", fmt.Errorf("relay hostname %q is not a DNS name", host)
	}
	return host, nil
}

// mintControlURL returns the Headscale URL devices should dial. Compose uses
// loopback. The appliance uses https://<relay hostname>, sets Headscale
// server_url to it and publishes the Caddy vhost that terminates TLS for it.
func (s stateConfig) mintControlURL() (string, error) {
	if s.runtime != "podman" {
		return composeControlURL, nil
	}
	host, err := s.relayHostname()
	if err != nil {
		return "", err
	}
	url := "https://" + host
	if err := s.applyControlPlanePublish(url); err != nil {
		return "", err
	}
	return url, nil
}

// loadPublishStatus returns the live control URL. Compose is localhost;
// the appliance reports https://<relay hostname>.
func (s stateConfig) loadPublishStatus() (publishStatus, error) {
	if s.runtime != "podman" {
		return publishStatus{
			ControlURL: composeControlURL,
			Hostname:   "localhost",
		}, nil
	}
	host, err := s.relayHostname()
	if err != nil {
		return publishStatus{}, err
	}
	return publishStatus{
		ControlURL: "https://" + host,
		Hostname:   host,
	}, nil
}

// applyControlPlanePublish updates Headscale server_url to controlURL, writes
// the Caddy vhost for its hostname and inserts any missing MagicDNS extra
// records for mesh modules.
func (s stateConfig) applyControlPlanePublish(controlURL string) error {
	host, err := controlHostname(controlURL)
	if err != nil {
		return err
	}
	headscaleChanged, err := s.writeHeadscaleServerURL(controlURL)
	if err != nil {
		return err
	}
	caddyChanged, err := s.writeHeadscaleCaddy(host)
	if err != nil {
		return err
	}
	if headscaleChanged {
		if err := runCmd("systemctl", "restart", "headscale.service"); err != nil {
			return fmt.Errorf("restart headscale: %w", err)
		}
	}
	if caddyChanged {
		if err := runCmd("systemctl", "restart", "caddy.service"); err != nil {
			return fmt.Errorf("restart caddy: %w", err)
		}
	}
	return nil
}

// writeHeadscaleCaddy writes the vhost that terminates TLS for host and
// proxies to Headscale, reporting whether the imported snippet changed.
func (s stateConfig) writeHeadscaleCaddy(host string) (bool, error) {
	path := filepath.Join(s.dir, "caddy", "headscale.caddy")
	updated := []byte(host + " {\n\treverse_proxy 127.0.0.1:8080\n}\n")
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read headscale vhost: %w", err)
	}
	if bytes.Equal(body, updated) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		return false, fmt.Errorf("write headscale vhost: %w", err)
	}
	return true, nil
}

// writeHeadscaleServerURL updates server_url and mesh extra records in the
// state Headscale config (seeding from /etc when absent).
func (s stateConfig) writeHeadscaleServerURL(controlURL string) (bool, error) {
	path := s.headscaleConfigPath()
	body, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("read headscale config: %w", err)
		}
		body, err = os.ReadFile(etcHeadscaleConfig)
		if err != nil {
			return false, fmt.Errorf("read %s: %w", etcHeadscaleConfig, err)
		}
	}
	updated, err := applyServerURL(body, controlURL)
	if err != nil {
		return false, err
	}
	updated, err = ensureExtraRecords(updated, meshExtraRecordNames(), meshExtraRecordIP)
	if err != nil {
		return false, err
	}
	if bytes.Equal(body, updated) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		return false, fmt.Errorf("write headscale config: %w", err)
	}
	return true, nil
}

// meshExtraRecordNames is the *.dadi names Caddy serves on the appliance.
func meshExtraRecordNames() []string {
	names := []string{"nas.dadi"}
	for _, t := range healthTargets("podman") {
		names = append(names, t.name+".dadi")
	}
	return names
}

// ensureExtraRecords inserts missing MagicDNS A records into a Headscale config body.
func ensureExtraRecords(config []byte, names []string, ip string) ([]byte, error) {
	if !bytes.Contains(config, []byte("extra_records:")) {
		return nil, fmt.Errorf("headscale config has no extra_records")
	}
	present := map[string]struct{}{}
	for _, m := range extraRecordNameRe.FindAllSubmatch(config, -1) {
		present[string(m[1])] = struct{}{}
	}
	var missing []string
	for _, name := range names {
		if _, ok := present[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return config, nil
	}
	insertAt, err := extraRecordsInsertIndex(config)
	if err != nil {
		return nil, err
	}
	var block strings.Builder
	for _, name := range missing {
		fmt.Fprintf(&block, "    - name: %q\n      type: \"A\"\n      value: %q\n", name, ip)
	}
	out := make([]byte, 0, len(config)+block.Len())
	out = append(out, config[:insertAt]...)
	out = append(out, block.String()...)
	out = append(out, config[insertAt:]...)
	return out, nil
}

// extraRecordsInsertIndex is the byte offset just after the extra_records list.
func extraRecordsInsertIndex(config []byte) (int, error) {
	lines := strings.SplitAfter(string(config), "\n")
	off := 0
	seen := false
	insert := 0
	for _, line := range lines {
		raw := strings.TrimRight(line, "\n")
		trimmed := strings.TrimSpace(raw)
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		if !seen {
			if trimmed == "extra_records:" {
				seen = true
				insert = off + len(line)
			}
			off += len(line)
			continue
		}
		if trimmed == "" || indent > 2 {
			insert = off + len(line)
			off += len(line)
			continue
		}
		return off, nil
	}
	if !seen {
		return 0, fmt.Errorf("headscale config has no extra_records")
	}
	return insert, nil
}
