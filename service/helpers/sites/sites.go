// Package sites keeps what Nas has learned about how each site behaves for the browser
// helper: how it signs in, which time zone it shows, where its due dates are listed and what
// is known to block it. One JSON file per host lives in Nas's own data directory; the files
// are data, never code, and are the same for every helper.
package sites

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Field names of a Profile, as note_site takes them.
const (
	FieldSignIn       = "sign_in"
	FieldTimeZone     = "time_zone_shown"
	FieldDueDatePages = "due_date_pages"
	FieldBlocks       = "blocks"
)

// Sign-in methods a profile records.
const (
	MethodPasskey = "passkey"
	MethodLogin   = "login"
	MethodDECS    = "decs"
)

// SignIn is how a site signs in. Item is the Chaavi item's title; a DECS login has none.
type SignIn struct {
	Method string `json:"method"`
	Item   string `json:"item,omitempty"`
}

// Page is a page of a site worth reading, such as a list of due dates.
type Page struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Block is a symptom known to stop work on a site, and the day it was noted (YYYY-MM-DD).
type Block struct {
	Symptom string `json:"symptom"`
	Since   string `json:"since"`
}

// Profile is everything known about one host.
type Profile struct {
	SignIn        *SignIn `json:"sign_in,omitempty"`
	TimeZoneShown string  `json:"time_zone_shown,omitempty"`
	DueDatePages  []Page  `json:"due_date_pages,omitempty"`
	Blocks        []Block `json:"blocks,omitempty"`
}

// Store is the profiles saved under one directory.
type Store struct {
	dir string
	now func() time.Time
	mu  sync.Mutex
}

// New returns the store over dir, creating it when missing. now dates new blocks.
func New(dir string, now func() time.Time) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("sites dir: %w", err)
	}
	return &Store{dir: dir, now: now}, nil
}

var hostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// path is where host's profile is saved, or an error when host is not a bare host name.
func (s *Store) path(host string) (string, error) {
	if !hostPattern.MatchString(host) {
		return "", fmt.Errorf("host %q must be a bare lowercase host name such as d2l.msu.edu", host)
	}
	return filepath.Join(s.dir, host+".json"), nil
}

// Get is the profile of host, and false when none is saved.
func (s *Store) Get(host string) (Profile, bool, error) {
	path, err := s.path(host)
	if err != nil {
		return Profile{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(path)
}

// read loads the profile at path.
func (s *Store) read(path string) (Profile, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Profile{}, false, nil
	}
	if err != nil {
		return Profile{}, false, fmt.Errorf("read site profile: %w", err)
	}
	var p Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		return Profile{}, false, fmt.Errorf("parse site profile %s: %w", filepath.Base(path), err)
	}
	return p, true, nil
}

// Note writes one field of host's profile. sign_in takes {method, item} and time_zone_shown a
// string, and both replace what was there. due_date_pages takes {name, url} and replaces the
// page of that name or adds one. blocks takes the symptom as a string and adds it with today's
// date unless it is already there. Any other field, or a value of the wrong shape, is an error.
func (s *Store) Note(host, field string, value json.RawMessage) error {
	path, err := s.path(host)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, _, err := s.read(path)
	if err != nil {
		return err
	}
	switch field {
	case FieldSignIn:
		var v SignIn
		if err := decodeStrict(value, &v); err != nil {
			return fmt.Errorf("sign_in takes {method, item}: %w", err)
		}
		v.Item = strings.TrimSpace(v.Item)
		switch {
		case v.Method != MethodPasskey && v.Method != MethodLogin && v.Method != MethodDECS:
			return fmt.Errorf("sign_in method must be %s, %s or %s", MethodPasskey, MethodLogin, MethodDECS)
		case v.Method != MethodDECS && v.Item == "":
			return fmt.Errorf("sign_in with a %s needs the Chaavi item's title", v.Method)
		}
		p.SignIn = &v
	case FieldTimeZone:
		var v string
		if err := decodeStrict(value, &v); err != nil || strings.TrimSpace(v) == "" {
			return errors.New("time_zone_shown takes a non-empty string")
		}
		p.TimeZoneShown = strings.TrimSpace(v)
	case FieldDueDatePages:
		var v Page
		if err := decodeStrict(value, &v); err != nil {
			return fmt.Errorf("due_date_pages takes {name, url}: %w", err)
		}
		u, err := url.ParseRequestURI(v.URL)
		if v.Name = strings.TrimSpace(v.Name); v.Name == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("due_date_pages needs a name and a full http(s) url")
		}
		replaced := false
		for i := range p.DueDatePages {
			if p.DueDatePages[i].Name == v.Name {
				p.DueDatePages[i], replaced = v, true
			}
		}
		if !replaced {
			p.DueDatePages = append(p.DueDatePages, v)
		}
	case FieldBlocks:
		var v string
		if err := decodeStrict(value, &v); err != nil || strings.TrimSpace(v) == "" {
			return errors.New("blocks takes the symptom as a non-empty string")
		}
		v = strings.TrimSpace(v)
		for _, b := range p.Blocks {
			if b.Symptom == v {
				return nil
			}
		}
		p.Blocks = append(p.Blocks, Block{Symptom: v, Since: s.now().Format("2006-01-02")})
	default:
		return fmt.Errorf("unknown field %q: a site profile holds %s, %s, %s and %s", field, FieldSignIn, FieldTimeZone, FieldDueDatePages, FieldBlocks)
	}
	return s.write(path, p)
}

// decodeStrict unmarshals raw into v, rejecting unknown keys and trailing data.
func decodeStrict(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}

// write saves p at path through a temp file and rename.
func (s *Store) write(path string, p Profile) error {
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("encode site profile: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, "site.tmp.*")
	if err != nil {
		return fmt.Errorf("create site profile temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write site profile temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close site profile temp: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write site profile: %w", err)
	}
	return nil
}

// Describe is the profile as the helper reads it, or "" when it holds nothing.
func Describe(host string, p Profile) string {
	var parts []string
	if p.SignIn != nil {
		line := "signs in by " + p.SignIn.Method
		if p.SignIn.Item != "" {
			line += " with the Chaavi item " + jsonString(p.SignIn.Item)
		} else {
			line += " (no Chaavi item)"
		}
		parts = append(parts, line)
	}
	if p.TimeZoneShown != "" {
		parts = append(parts, "shows times in "+p.TimeZoneShown)
	}
	for _, page := range p.DueDatePages {
		parts = append(parts, fmt.Sprintf("due dates: %s at %s", page.Name, page.URL))
	}
	for _, b := range p.Blocks {
		parts = append(parts, fmt.Sprintf("known block since %s: %s", b.Since, b.Symptom))
	}
	if len(parts) == 0 {
		return ""
	}
	return "[site profile " + host + "] " + strings.Join(parts, "; ")
}

// jsonString quotes s.
func jsonString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
