package browser

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// sitesFile records, in an agent's folder of Chromium folders, when each folder last held
// each site.
const sitesFile = "sites.json"

// folderState is one of an agent's Chromium folders as the choice sees it.
type folderState struct {
	Name  string
	InUse bool
	// Sites maps a site to when this folder last held it.
	Sites map[string]time.Time
}

// chooseFolder picks the folder a new browser of an agent runs on: its own folder when it is
// free (a woken task keeps its folder), else the free folder that most recently held any of
// sites, else the first free one. When every folder is busy it returns create true and the
// name of the next folder to make.
func chooseFolder(folders []folderState, own string, sites []string) (name string, create bool) {
	sorted := append([]folderState(nil), folders...)
	sort.Slice(sorted, func(i, j int) bool { return folderNumber(sorted[i].Name) < folderNumber(sorted[j].Name) })
	var free []folderState
	for _, f := range sorted {
		if !f.InUse {
			free = append(free, f)
		}
	}
	for _, f := range free {
		if f.Name == own && own != "" {
			return own, false
		}
	}
	var best string
	var bestAt time.Time
	for _, f := range free {
		for _, site := range sites {
			if at, ok := f.Sites[site]; ok && at.After(bestAt) {
				best, bestAt = f.Name, at
			}
		}
	}
	if best != "" {
		return best, false
	}
	if len(free) > 0 {
		return free[0].Name, false
	}
	next := 1
	for _, f := range sorted {
		if n := folderNumber(f.Name); n >= next {
			next = n + 1
		}
	}
	return strconv.Itoa(next), true
}

// folderNumber is the number a folder is named by, or 0.
func folderNumber(name string) int {
	n, _ := strconv.Atoi(name)
	return n
}

var twoPartTLD = map[string]bool{"co": true, "com": true, "org": true, "net": true, "gov": true, "edu": true, "ac": true}

// siteOf is the registrable domain of a URL or host (login.example.co.uk gives example.co.uk),
// or "" when it names no site.
func siteOf(raw string) string {
	host := raw
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		host = u.Hostname()
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	labels := strings.Split(host, ".")
	if len(labels) < 2 || host == "" {
		return ""
	}
	keep := 2
	if len(labels) >= 3 && len(labels[len(labels)-1]) == 2 && twoPartTLD[labels[len(labels)-2]] {
		keep = 3
	}
	return strings.Join(labels[len(labels)-keep:], ".")
}

var hostPattern = regexp.MustCompile(`(?i)\b(?:[a-z0-9-]+\.)+[a-z]{2,}\b`)

// sitesIn is the sites a text names, in the order they first appear.
func sitesIn(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range hostPattern.FindAllString(text, -1) {
		if site := siteOf(m); site != "" && !seen[site] {
			seen[site] = true
			out = append(out, site)
		}
	}
	return out
}

// agentFolders lists agent's Chromium folders under dir with the sites each last held, and
// which are in use.
func agentFolders(dir, agent string, inUse func(string) bool) ([]folderState, error) {
	base := filepath.Join(dir, agent)
	entries, err := os.ReadDir(base)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	sites, err := loadSites(base)
	if err != nil {
		return nil, err
	}
	var out []folderState
	for _, e := range entries {
		if e.IsDir() && folderNumber(e.Name()) > 0 {
			out = append(out, folderState{Name: e.Name(), InUse: inUse(e.Name()), Sites: sites[e.Name()]})
		}
	}
	return out, nil
}

// loadSites reads the sites record under base; none yet is empty.
func loadSites(base string) (map[string]map[string]time.Time, error) {
	raw, err := os.ReadFile(filepath.Join(base, sitesFile))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]map[string]time.Time{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out map[string]map[string]time.Time
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Join(base, sitesFile), err)
	}
	return out, nil
}

// recordSites notes that folder of agent held each site at now.
func recordSites(dir, agent, folder string, sites []string, now time.Time) error {
	base := filepath.Join(dir, agent)
	all, err := loadSites(base)
	if err != nil {
		return err
	}
	if all[folder] == nil {
		all[folder] = map[string]time.Time{}
	}
	for _, s := range sites {
		all[folder][s] = now
	}
	raw, err := json.Marshal(all)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(base, sitesFile+".tmp")
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(base, sitesFile))
}
