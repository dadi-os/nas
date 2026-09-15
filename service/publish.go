package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const etcHeadscaleConfig = "/etc/headscale/config.yaml"

var (
	srcIPv4Re     = regexp.MustCompile(`\bsrc (\d{1,3}(?:\.\d{1,3}){3})\b`)
	upnpExtIPRe   = regexp.MustCompile(`(?i)ExternalIPAddress\s*=\s*(\d{1,3}(?:\.\d{1,3}){3})`)
	upnpExtAltRe  = regexp.MustCompile(`(?i)External IP address[^:]*:\s*(\d{1,3}(?:\.\d{1,3}){3})`)
	serverURLLine = regexp.MustCompile(`(?m)^server_url:\s*.*$`)
)

// controlHostname is the hostname Hath/Tailscale dial from a control plane URL.
func controlHostname(controlURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(controlURL))
	if err != nil {
		return "", fmt.Errorf("parse control URL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("control URL has no hostname")
	}
	if net.ParseIP(host) != nil {
		return "", fmt.Errorf("control URL must be a hostname, not an IP")
	}
	return host, nil
}

// validateControlURL enforces https on the appliance; compose may use http for local Headscale.
func validateControlURL(runtime, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("control URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse control URL: %w", err)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if runtime == "podman" {
			return fmt.Errorf("control URL must be https on the appliance")
		}
	default:
		return fmt.Errorf("control URL must be http(s)")
	}
	if _, err := controlHostname(raw); err != nil {
		return err
	}
	return nil
}

// caddyHeadscaleSnippet is the public HTTPS vhost Caddy imports for Headscale.
func caddyHeadscaleSnippet(host string) string {
	return fmt.Sprintf("https://%s {\n\treverse_proxy 127.0.0.1:8080\n}\n", host)
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

// parseUPnPExternalIP extracts the WAN IPv4 from `upnpc -s` output (miniupnpc variants).
func parseUPnPExternalIP(out string) (string, error) {
	if m := upnpExtIPRe.FindStringSubmatch(out); m != nil {
		return m[1], nil
	}
	if m := upnpExtAltRe.FindStringSubmatch(out); m != nil {
		return m[1], nil
	}
	return "", fmt.Errorf("UPnP output has no external IPv4")
}

// caddySnippetPath is the imported Caddyfile fragment for the public Headscale vhost.
func (s stateConfig) caddySnippetPath() string {
	return filepath.Join(s.dir, "caddy", "headscale.caddy")
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
	WANIP      string `json:"wan_ip,omitempty"`
}

// loadPublishStatus returns the configured control URL and, on the appliance, live LAN/WAN IPs.
// When no control URL is set yet, ControlURL is empty and LAN/WAN are still filled on podman.
func (s stateConfig) loadPublishStatus() (publishStatus, error) {
	st := publishStatus{}
	url, err := s.resolveControlURL()
	if err != nil {
		if !errors.Is(err, errControlURLUnset) {
			return st, err
		}
	} else {
		st.ControlURL = url
		host, herr := controlHostname(url)
		if herr != nil {
			return st, herr
		}
		st.Hostname = host
	}
	if s.runtime != "podman" {
		return st, nil
	}
	lan, err := lanIPv4()
	if err != nil {
		return st, err
	}
	st.LANIP = lan
	wan, err := upnpExternalIP()
	if err != nil {
		return st, err
	}
	st.WANIP = wan
	return st, nil
}

// applyControlPlanePublish maps 80/443 via UPnP, then writes Caddy + Headscale and reloads them.
func (s stateConfig) applyControlPlanePublish(controlURL string) error {
	host, err := controlHostname(controlURL)
	if err != nil {
		return err
	}
	if err := s.mapHeadscalePorts(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.caddySnippetPath()), 0o700); err != nil {
		return err
	}
	snippet := caddyHeadscaleSnippet(host)
	if err := os.WriteFile(s.caddySnippetPath(), []byte(snippet), 0o644); err != nil {
		return fmt.Errorf("write caddy snippet: %w", err)
	}
	if err := s.writeHeadscaleServerURL(controlURL); err != nil {
		return err
	}
	if err := runCmd("systemctl", "restart", "caddy.service"); err != nil {
		return fmt.Errorf("restart caddy: %w", err)
	}
	if err := runCmd("systemctl", "restart", "headscale.service"); err != nil {
		return fmt.Errorf("restart headscale: %w", err)
	}
	return nil
}

// writeHeadscaleServerURL updates server_url in the state Headscale config (seeding from /etc when absent).
func (s stateConfig) writeHeadscaleServerURL(controlURL string) error {
	path := s.headscaleConfigPath()
	body, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read headscale config: %w", err)
		}
		body, err = os.ReadFile(etcHeadscaleConfig)
		if err != nil {
			return fmt.Errorf("read %s: %w", etcHeadscaleConfig, err)
		}
	}
	updated, err := applyServerURL(body, controlURL)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		return fmt.Errorf("write headscale config: %w", err)
	}
	return nil
}

// mapHeadscalePorts adds UPnP TCP mappings for 80 and 443 to this host's LAN address.
func (s stateConfig) mapHeadscalePorts() error {
	lan, err := lanIPv4()
	if err != nil {
		return err
	}
	if err := upnpcAdd(lan, 443, "dadi-headscale-https"); err != nil {
		return err
	}
	if err := upnpcAdd(lan, 80, "dadi-headscale-http"); err != nil {
		return err
	}
	return nil
}

// lanIPv4 returns the IPv4 used as the source toward the public internet.
func lanIPv4() (string, error) {
	out, err := exec.Command("ip", "-4", "route", "get", "1.1.1.1").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ip route get: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return parseRouteSrcIPv4(string(out))
}

// upnpExternalIP returns the WAN IPv4 reported by the local IGD via upnpc.
func upnpExternalIP() (string, error) {
	out, err := exec.Command("upnpc", "-s").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("upnpc -s: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return parseUPnPExternalIP(string(out))
}

// upnpcAdd maps an external TCP port to lan:port through the IGD.
func upnpcAdd(lan string, port int, desc string) error {
	out, err := exec.Command(
		"upnpc",
		"-e", desc,
		"-a", lan, fmt.Sprintf("%d", port), fmt.Sprintf("%d", port), "tcp",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("UPnP map %d: %w (%s)", port, err, strings.TrimSpace(string(out)))
	}
	return nil
}
