package main

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const composeControlURL = "http://localhost:8080"

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
		return "", fmt.Errorf("control URL has no host")
	}
	return host, nil
}

// applianceControlURL is the public Headscale URL minted from the current WAN IPv4.
func applianceControlURL(wan string) string {
	return "http://" + wan + ":8080"
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

// mintControlURL returns the Headscale URL Hath should dial. Compose uses
// loopback. The appliance discovers WAN, maps TCP 8080 via UPnP, and sets
// Headscale server_url to http://<wan>:8080.
func (s stateConfig) mintControlURL() (string, error) {
	if s.runtime != "podman" {
		return composeControlURL, nil
	}
	wan, err := publicIPv4()
	if err != nil {
		return "", err
	}
	url := applianceControlURL(wan)
	if err := s.applyControlPlanePublish(url); err != nil {
		return "", err
	}
	return url, nil
}

// loadPublishStatus returns the live control URL. Compose is localhost;
// the appliance reports the current WAN-derived http://<wan>:8080 plus LAN/WAN IPs.
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
	wan, err := publicIPv4()
	if err != nil {
		return publishStatus{}, err
	}
	url := applianceControlURL(wan)
	host, err := controlHostname(url)
	if err != nil {
		return publishStatus{}, err
	}
	return publishStatus{
		ControlURL: url,
		Hostname:   host,
		LANIP:      lan,
		WANIP:      wan,
	}, nil
}

// applyControlPlanePublish maps Headscale 8080 via UPnP and updates server_url.
func (s stateConfig) applyControlPlanePublish(controlURL string) error {
	if _, err := controlHostname(controlURL); err != nil {
		return err
	}
	if err := s.mapHeadscalePorts(); err != nil {
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

// writeHeadscaleServerURL updates server_url in the state Headscale config (seeding from /etc when absent).
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

// mapHeadscalePorts adds a UPnP TCP mapping for Headscale (8080) to this host.
func (s stateConfig) mapHeadscalePorts() error {
	lan, err := lanIPv4()
	if err != nil {
		return err
	}
	return upnpcAdd(lan, 8080, "dadi-headscale")
}

// publicIPv4 returns the WAN IPv4 as seen from the public internet.
func publicIPv4() (string, error) {
	out, err := exec.Command("curl", "-4", "-fsS", "--max-time", "8", "https://ifconfig.me").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("public IPv4: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	ip := strings.TrimSpace(string(out))
	if net.ParseIP(ip) == nil || net.ParseIP(ip).To4() == nil {
		return "", fmt.Errorf("public IPv4: not an IPv4 address: %q", ip)
	}
	return ip, nil
}

// lanIPv4 returns the IPv4 used as the source toward the public internet.
func lanIPv4() (string, error) {
	out, err := exec.Command("ip", "-4", "route", "get", "1.1.1.1").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ip route get: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return parseRouteSrcIPv4(string(out))
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
