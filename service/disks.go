package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// diskVolumeStatus is one physical disk's primary mounted filesystem usage.
type diskVolumeStatus struct {
	Name        string  `json:"name"`
	Model       string  `json:"model,omitempty"`
	Transport   string  `json:"transport,omitempty"`
	Mount       string  `json:"mount"`
	FreeBytes   uint64  `json:"free_bytes"`
	TotalBytes  uint64  `json:"total_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

type lsblkDevice struct {
	Name        string        `json:"name"`
	Type        string        `json:"type"`
	Size        json.Number   `json:"size"`
	Fstype      string        `json:"fstype"`
	Mountpoints []any         `json:"mountpoints"`
	Model       string        `json:"model"`
	Tran        string        `json:"tran"`
	Children    []lsblkDevice `json:"children"`
}

type lsblkTree struct {
	Blockdevices []lsblkDevice `json:"blockdevices"`
}

// readDisks lists each physical disk that has a mounted local filesystem,
// using the largest mounted volume on that disk for capacity.
func readDisks() ([]diskVolumeStatus, error) {
	out, err := exec.Command(
		"lsblk", "-J", "-b", "-o", "NAME,TYPE,SIZE,FSTYPE,MOUNTPOINTS,MODEL,TRAN",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("lsblk: %w", err)
	}
	var tree lsblkTree
	if err := json.Unmarshal(out, &tree); err != nil {
		return nil, fmt.Errorf("lsblk json: %w", err)
	}

	var disks []diskVolumeStatus
	for _, d := range tree.Blockdevices {
		if d.Type != "disk" {
			continue
		}
		mounts := collectMounts(d)
		bestMount := ""
		var best diskStatus
		for _, m := range mounts {
			if skipMount(m) {
				continue
			}
			st, err := readDisk(m)
			if err != nil || st.TotalBytes == 0 {
				continue
			}
			if bestMount == "" || st.TotalBytes > best.TotalBytes ||
				(st.TotalBytes == best.TotalBytes && mountPref(m) < mountPref(bestMount)) {
				best = st
				bestMount = m
			}
		}
		if bestMount == "" {
			continue
		}
		disks = append(disks, diskVolumeStatus{
			Name:        d.Name,
			Model:       strings.TrimSpace(d.Model),
			Transport:   strings.TrimSpace(d.Tran),
			Mount:       bestMount,
			FreeBytes:   best.FreeBytes,
			TotalBytes:  best.TotalBytes,
			UsedPercent: best.UsedPercent,
		})
	}
	sort.Slice(disks, func(i, j int) bool {
		ti, tj := diskSortKey(disks[i]), diskSortKey(disks[j])
		if ti != tj {
			return ti < tj
		}
		return disks[i].Name < disks[j].Name
	})
	return disks, nil
}

func diskSortKey(d diskVolumeStatus) int {
	t := strings.ToLower(d.Transport)
	switch {
	case t == "nvme" || strings.HasPrefix(d.Name, "nvme"):
		return 0
	case t == "sata" || t == "ata":
		return 1
	default:
		return 2
	}
}

func collectMounts(d lsblkDevice) []string {
	seen := map[string]struct{}{}
	var out []string
	var walk func(lsblkDevice)
	walk = func(n lsblkDevice) {
		for _, raw := range n.Mountpoints {
			m, ok := raw.(string)
			if !ok {
				continue
			}
			m = strings.TrimSpace(m)
			if m == "" || m == "[SWAP]" {
				continue
			}
			if _, exists := seen[m]; exists {
				continue
			}
			seen[m] = struct{}{}
			out = append(out, m)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(d)
	return out
}

func skipMount(m string) bool {
	switch m {
	case "/boot", "/boot/efi":
		return true
	}
	if strings.HasPrefix(m, "/run/") || strings.HasPrefix(m, "/sys/") || strings.HasPrefix(m, "/proc/") {
		return true
	}
	if strings.Contains(m, "/containers/storage/overlay") {
		return true
	}
	return false
}

// mountPref ranks mounts when capacities tie — prefer OS roots over data paths.
func mountPref(m string) int {
	switch m {
	case "/":
		return 0
	case "/sysroot":
		return 1
	case "/var":
		return 2
	default:
		return 3
	}
}

// diskLabel builds a short UI name from model/transport/device.
func diskLabel(d diskVolumeStatus) string {
	if d.Model != "" {
		return d.Model
	}
	if d.Transport != "" {
		return fmt.Sprintf("%s (%s)", d.Name, d.Transport)
	}
	return d.Name
}
