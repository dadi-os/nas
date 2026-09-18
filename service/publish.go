package main

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
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
	srcIPv4Re         = regexp.MustCompile(`\bsrc (\d{1,3}(?:\.\d{1,3}){3})\b`)
	serverURLLine     = regexp.MustCompile(`(?m)^server_url:\s*.*$`)
	extraRecordNameRe = regexp.MustCompile(`(?m)^\s+-\s+name:\s+"([^"]+)"`)
)

// controlHostname is the hostname Hath/Tailscale dial from a control plane URL.
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

// applianceControlURL is the Headscale URL Hath dials at the given IPv4.
func applianceControlURL(ip string) string {
	return "http://" + ip + ":8080"
}

// applyServerURL sets Headscale server_url in a config.yaml body.
func applyServerURL(config []byte, controlURL string) ([]byte, error) {
	if !serverURLLine.Match(config) {
		return nil, fmt.Errorf("headscale config has no server_url line")
	}
	repl := []byte("server_url: " + strings.TrimSpace(controlURL))
	return serverURLLine.ReplaceAll(config, repl), nil
}

// parseRouteSrcIPv4 extracts the source IPv4 from `ip route get` output.
func parseRouteSrcIPv4(routeGet string) (string, error) {
	m := srcIPv4Re.FindStringSubmatch(routeGet)
	if m == nil {
		return "", fmt.Errorf("no src IPv4 in ip route get")
	}
	return m[1], nil
}

// headscaleConfigPath is the writable Headscale config under DADI_STATE_DIR.
func (s stateConfig) headscaleConfigPath() string {
	return filepath.Join(s.dir, "headscale", "config.yaml")
}

// publishStatus is GET /headscale/publish.
type publishStatus struct {
	ControlURL string `json:"control_url"`
	Hostname   string `json:"hostname,omitempty"`
	LANIP      string `json:"lan_ip,omitempty"`
}

// mintControlURL returns the Headscale URL Hath should dial. Compose uses
// loopback. The appliance uses http://<lan>:8080 from the host LAN IPv4
// and sets Headscale server_url to that URL.
func (s stateConfig) mintControlURL() (string, error) {
	if s.runtime != "podman" {
		return composeControlURL, nil
	}
	lan, err := lanIPv4()
	if err != nil {
		return "", err
	}
	url := applianceControlURL(lan)
	if err := s.applyControlPlanePublish(url); err != nil {
		return "", err
	}
	return url, nil
}

// loadPublishStatus returns the live control URL. Compose is localhost;
// the appliance reports http://<lan>:8080.
func (s stateConfig) loadPublishStatus() (publishStatus, error) {
	if s.runtime != "podman" {
		return publishStatus{
			ControlURL: composeControlURL,
			Hostname:   "localhost",
		}, nil
	}
	lan, err := lanIPv4()
	if err != nil {
		return publishStatus{}, err
	}
	url := applianceControlURL(lan)
	host, err := controlHostname(url)
	if err != nil {
		return publishStatus{}, err
	}
	return publishStatus{
		ControlURL: url,
		Hostname:   host,
		LANIP:      lan,
	}, nil
}

// applyControlPlanePublish updates Headscale server_url to controlURL and
// inserts any missing MagicDNS extra records for mesh modules.
func (s stateConfig) applyControlPlanePublish(controlURL string) error {
	if _, err := controlHostname(controlURL); err != nil {
		return err
	}
	headscaleChanged, err := s.writeHeadscaleServerURL(controlURL)
	if err != nil {
		return err
	}
	if headscaleChanged {
		if err := runCmd("systemctl", "restart", "headscale.service"); err != nil {
			return fmt.Errorf("restart headscale: %w", err)
		}
	}
	return nil
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

// lanIPv4 returns the IPv4 used as the source toward the public internet.
func lanIPv4() (string, error) {
	out, err := exec.Command("ip", "-4", "route", "get", "1.1.1.1").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ip route get: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return parseRouteSrcIPv4(string(out))
}
