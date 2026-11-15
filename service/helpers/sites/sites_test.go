package sites

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(dir, func() time.Time { return time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func note(t *testing.T, s *Store, host, field, value string) error {
	t.Helper()
	return s.Note(host, field, json.RawMessage(value))
}

func TestNoteRejectsAnUnknownFieldAndABadValue(t *testing.T) {
	s, dir := newStore(t)
	for name, tc := range map[string][3]string{
		"unknown field":       {"d2l.msu.edu", "mood", `"fine"`},
		"unknown sign_in key": {"d2l.msu.edu", "sign_in", `{"method":"passkey","item":"MSU","extra":1}`},
		"bad method":          {"d2l.msu.edu", "sign_in", `{"method":"magic","item":"MSU"}`},
		"passkey no item":     {"d2l.msu.edu", "sign_in", `{"method":"passkey"}`},
		"empty zone":          {"d2l.msu.edu", "time_zone_shown", `""`},
		"page without url":    {"d2l.msu.edu", "due_date_pages", `{"name":"Calendar"}`},
		"block not a string":  {"d2l.msu.edu", "blocks", `{"symptom":"x"}`},
		"host with a path":    {"d2l.msu.edu/home", "time_zone_shown", `"Eastern"`},
		"host not a host":     {"localhost", "time_zone_shown", `"Eastern"`},
	} {
		if err := note(t, s, tc[0], tc[1], tc[2]); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("rejected notes left %d files", len(entries))
	}
}

func TestNoteSavesEachFieldAndDatesABlock(t *testing.T) {
	s, dir := newStore(t)
	host := "cse335.egr.msu.edu"
	for field, value := range map[string]string{
		"sign_in":         `{"method":"decs"}`,
		"time_zone_shown": `"Eastern"`,
		"due_date_pages":  `{"name":"Schedule","url":"https://cse335.egr.msu.edu/schedule"}`,
		"blocks":          `"VPN needed off campus"`,
	} {
		if err := note(t, s, host, field, value); err != nil {
			t.Fatalf("%s: %v", field, err)
		}
	}
	if err := note(t, s, host, "blocks", `"VPN needed off campus"`); err != nil {
		t.Fatal(err)
	}
	if err := note(t, s, host, "due_date_pages", `{"name":"Schedule","url":"https://cse335.egr.msu.edu/new"}`); err != nil {
		t.Fatal(err)
	}
	p, found, err := s.Get(host)
	if err != nil || !found {
		t.Fatalf("get: %v %v", found, err)
	}
	if p.SignIn.Method != MethodDECS || p.SignIn.Item != "" || p.TimeZoneShown != "Eastern" {
		t.Fatalf("profile %+v", p)
	}
	if len(p.DueDatePages) != 1 || p.DueDatePages[0].URL != "https://cse335.egr.msu.edu/new" {
		t.Fatalf("due date pages %+v", p.DueDatePages)
	}
	if len(p.Blocks) != 1 || p.Blocks[0].Since != "2026-11-14" {
		t.Fatalf("blocks %+v", p.Blocks)
	}
	if _, err := os.Stat(filepath.Join(dir, host+".json")); err != nil {
		t.Fatalf("one file per host: %v", err)
	}
	text := Describe(host, p)
	for _, want := range []string{"[site profile " + host + "]", "decs", "Eastern", "known block since 2026-11-14: VPN needed off campus"} {
		if !strings.Contains(text, want) {
			t.Errorf("describe lacks %q: %s", want, text)
		}
	}
}

func TestReplaceDueDatePagesReplacesEachNamedHostsPagesAndChecksBeforeWriting(t *testing.T) {
	s, _ := newStore(t)
	host := "d2l.msu.edu"
	if err := note(t, s, host, "due_date_pages", `{"name":"Old","url":"https://d2l.msu.edu/old"}`); err != nil {
		t.Fatal(err)
	}
	if err := note(t, s, host, "blocks", `"VPN"`); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceDueDatePages([]Page{{Name: "Calendar", URL: "https://d2l.msu.edu/cal"}, {Name: "Quizzes", URL: "https://D2L.msu.edu/q"}, {Name: "Schedule", URL: "https://cse335.egr.msu.edu/s"}}); err != nil {
		t.Fatal(err)
	}
	p, _, _ := s.Get(host)
	if len(p.DueDatePages) != 2 || p.DueDatePages[0].Name != "Calendar" || p.DueDatePages[1].Name != "Quizzes" || len(p.Blocks) != 1 {
		t.Fatalf("profile %+v", p)
	}
	other, found, _ := s.Get("cse335.egr.msu.edu")
	if !found || len(other.DueDatePages) != 1 {
		t.Fatalf("second host %+v", other)
	}
	if err := s.ReplaceDueDatePages([]Page{{Name: "New", URL: "https://d2l.msu.edu/new"}, {Name: "Bad", URL: "not a url"}}); err == nil {
		t.Fatal("a bad page was accepted")
	}
	if p, _, _ := s.Get(host); len(p.DueDatePages) != 2 {
		t.Fatalf("a failed replace wrote: %+v", p.DueDatePages)
	}
	if err := s.ReplaceDueDatePages(nil); err != nil {
		t.Fatal(err)
	}
}

func TestGetOfAnUnknownHostFindsNothing(t *testing.T) {
	s, _ := newStore(t)
	if _, found, err := s.Get("example.org"); found || err != nil {
		t.Fatalf("%v %v", found, err)
	}
}
