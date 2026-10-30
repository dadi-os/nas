package browser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestChooseFolder(t *testing.T) {
	now := time.Now()
	folders := []folderState{
		{Name: "1", Sites: map[string]time.Time{"linkedin.com": now.Add(-48 * time.Hour)}},
		{Name: "2", Sites: map[string]time.Time{"linkedin.com": now.Add(-time.Hour), "chase.com": now}},
		{Name: "3", InUse: true, Sites: map[string]time.Time{"linkedin.com": now}},
	}
	for _, tc := range []struct {
		name       string
		folders    []folderState
		own        string
		sites      []string
		want       string
		wantCreate bool
	}{
		{"free folder that most recently held the site", folders, "", []string{"linkedin.com"}, "2", false},
		{"busy folder is never chosen though it held the site last", folders, "", []string{"linkedin.com"}, "2", false},
		{"the task's own free folder wins", folders, "1", []string{"linkedin.com"}, "1", false},
		{"own folder in use falls back to the site", folders, "3", []string{"linkedin.com"}, "2", false},
		{"unknown site takes the first free", folders, "", []string{"nowhere.org"}, "1", false},
		{"no site takes the first free", folders, "", nil, "1", false},
		{"all busy makes the next", []folderState{{Name: "1", InUse: true}, {Name: "2", InUse: true}}, "", nil, "3", true},
		{"none yet makes the first", nil, "", nil, "1", true},
		{"numeric order, not lexical", []folderState{{Name: "10"}, {Name: "9"}}, "", nil, "9", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, create := chooseFolder(tc.folders, tc.own, tc.sites)
			if got != tc.want || create != tc.wantCreate {
				t.Fatalf("chose %q (create %v), want %q (create %v)", got, create, tc.want, tc.wantCreate)
			}
		})
	}
}

func TestSiteOfAndSitesIn(t *testing.T) {
	for in, want := range map[string]string{
		"https://login.www.linkedin.com/feed": "linkedin.com",
		"https://www.bbc.co.uk/news":          "bbc.co.uk",
		"http://localhost:8080/":              "",
		"example.com":                         "example.com",
		"not a url":                           "",
	} {
		if got := siteOf(in); got != want {
			t.Errorf("siteOf(%q) = %q, want %q", in, got, want)
		}
	}
	got := sitesIn("Check my Chase balance at https://secure.chase.com/web and also linkedin.com messages, then chase.com again")
	if !reflect.DeepEqual(got, []string{"chase.com", "linkedin.com"}) {
		t.Errorf("sitesIn = %v", got)
	}
}

func TestFoldersPersistAndAreListed(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"1", "2"} {
		if err := os.MkdirAll(filepath.Join(dir, "agent", f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "agent", "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	if err := recordSites(dir, "agent", "2", []string{"chase.com"}, at); err != nil {
		t.Fatal(err)
	}
	folders, err := agentFolders(dir, "agent", func(f string) bool { return f == "1" })
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 2 || !folders[0].InUse || folders[1].InUse || !folders[1].Sites["chase.com"].Equal(at) {
		t.Fatalf("folders %+v", folders)
	}
	none, err := agentFolders(dir, "new-agent", func(string) bool { return false })
	if err != nil || len(none) != 0 {
		t.Fatalf("an agent with no folders: %v %v", none, err)
	}
}

func TestMemoryCheck(t *testing.T) {
	write := func(dir, max, current string) {
		for name, v := range map[string]string{"memory.max": max, "memory.current": current} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(v+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir := t.TempDir()
	write(dir, "4294967296", "3000000000")
	m, err := readCgroup(dir)
	if err != nil || m.Max != 4294967296 || m.Current != 3000000000 {
		t.Fatalf("read: %+v %v", m, err)
	}
	if !m.hasRoom(1 << 30) {
		t.Fatal("no room for 1 GiB with 1.29 GB free")
	}
	if m.hasRoom(2 << 30) {
		t.Fatal("room for 2 GiB with 1.29 GB free")
	}
	write(dir, "max", "9999999999")
	if m, err = readCgroup(dir); err != nil || !m.hasRoom(browserReserve) {
		t.Fatalf("unlimited: %+v %v", m, err)
	}
	write(dir, "garbage", "1")
	if _, err := readCgroup(dir); err == nil {
		t.Fatal("parsed a garbage limit")
	}
	if _, err := readCgroup(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("read a cgroup that does not exist")
	}
}

func TestKitFailsAtStartupWithoutACgroup(t *testing.T) {
	if _, err := New(nil, "http://chaavi", "http://dwar", t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "absent")); err == nil || !strings.Contains(err.Error(), "BROWSER_CGROUP") {
		t.Fatalf("New with a missing cgroup: %v", err)
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "memory.max"), []byte("max\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "memory.current"), []byte("1\n"), 0o644)
	k, err := New(nil, "http://chaavi", "http://dwar", t.TempDir(), t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if room, err := k.Room(); err != nil || !room {
		t.Fatalf("Room: %v %v", room, err)
	}
	_ = os.WriteFile(filepath.Join(dir, "memory.max"), []byte("1000\n"), 0o644)
	if room, err := k.Room(); err != nil || room {
		t.Fatalf("Room with no memory left: %v %v", room, err)
	}
}

func TestClipText(t *testing.T) {
	long := strings.Repeat("line of text\n", 100)
	got := clipText(long, 200, "narrow it")
	if len(got) > 200+80 || !strings.Contains(got, "cut at") || !strings.HasSuffix(got, "narrow it") {
		t.Fatalf("clip: %q", got)
	}
	if clipText("short", 200, "x") != "short" {
		t.Fatal("clipped a short text")
	}
}

func TestParseKey(t *testing.T) {
	def, mods, err := parseKey("Control+Shift+ArrowDown")
	if err != nil || def.key != "ArrowDown" || mods != 2|8 {
		t.Fatalf("chord: %+v %d %v", def, mods, err)
	}
	if def, _, err = parseKey("a"); err != nil || def.code != "KeyA" || def.text != "a" {
		t.Fatalf("letter: %+v %v", def, err)
	}
	if def, _, err = parseKey("F5"); err != nil || def.vk != 116 {
		t.Fatalf("function key: %+v %v", def, err)
	}
	if _, _, err = parseKey("Hyper+x"); err == nil {
		t.Fatal("accepted an unknown modifier")
	}
	if _, _, err = parseKey("NoSuchKey"); err == nil {
		t.Fatal("accepted an unknown key")
	}
}

func TestDiffLines(t *testing.T) {
	before := map[string]int{"heading": 1, "button [e1] Reveal": 1, "text a": 2}
	added, gone := diffLines(before, "heading\nbutton [e1] Reveal\ntext a\nbutton [e2] Confirm")
	if !reflect.DeepEqual(added, []string{"button [e2] Confirm"}) || gone != 1 {
		t.Fatalf("added %v gone %d", added, gone)
	}
	if added, gone = diffLines(before, "heading\nbutton [e1] Reveal\ntext a\ntext a"); len(added) != 0 || gone != 0 {
		t.Fatalf("unchanged page: added %v gone %d", added, gone)
	}
}

func TestActionsDecodeAndNeverNameTheirText(t *testing.T) {
	var in input
	raw := `{"actions":[{"type":"type","ref":"e4","text":"s3cret","submit":true},{"type":"check","ref":"e5","checked":false},{"type":"key","key":"Escape"}]}`
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	if len(in.Actions) != 3 || !in.Actions[0].Submit || in.Actions[1].Checked == nil || *in.Actions[1].Checked {
		t.Fatalf("decoded %+v", in.Actions)
	}
	for _, a := range in.Actions {
		if strings.Contains(a.String(), "s3cret") {
			t.Fatalf("action names its text: %s", a)
		}
	}
	if got := in.Actions[2].String(); got != "key Escape" {
		t.Fatalf("string %q", got)
	}
}

func TestRenderTableAsCSVAndJSON(t *testing.T) {
	tab := Table{Kind: "table", Headers: []string{"Date", "Payee"}, Rows: [][]string{{"2026-10-01", "A, Inc"}, {"2026-10-02", "B"}}}
	csv, err := render(tab, "table", 1)
	if err != nil || csv.Data != "Date,Payee\n2026-10-01,\"A, Inc\"\n2026-10-02,B\n" || csv.Rows != 2 || csv.Ext != "csv" {
		t.Fatalf("csv: %+v %v", csv, err)
	}
	js, err := render(tab, "json", 1)
	if err != nil {
		t.Fatal(err)
	}
	var objects []map[string]string
	if err := json.Unmarshal([]byte(js.Data), &objects); err != nil || objects[0]["Payee"] != "A, Inc" || len(objects) != 2 {
		t.Fatalf("json: %s %v", js.Data, err)
	}
}

func TestUniquePath(t *testing.T) {
	dir := t.TempDir()
	first, err := uniquePath(dir, "report.csv")
	if err != nil || first != filepath.Join(dir, "report.csv") {
		t.Fatalf("first: %q %v", first, err)
	}
	_ = os.WriteFile(first, nil, 0o644)
	second, err := uniquePath(dir, "report.csv")
	if err != nil || second != filepath.Join(dir, "report (1).csv") {
		t.Fatalf("second: %q %v", second, err)
	}
	if p, err := uniquePath(dir, "../../etc/passwd"); err != nil || filepath.Dir(p) != dir {
		t.Fatalf("a suggested name escaped the downloads dir: %q %v", p, err)
	}
}

func TestToolsAreTheSpecifiedOnes(t *testing.T) {
	k := &Kit{}
	var names []string
	for _, tool := range k.Tools() {
		names = append(names, tool.Name)
	}
	want := []string{"look", "act", "find", "extract", "screenshot", "tabs", "navigate", "sign_in", "download", "upload"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
	if k.Kind() != "browser" || k.IdleSleep() != 15*time.Minute || !strings.Contains(k.Charter(), "sign_in") {
		t.Fatal("kit identity")
	}
}

func TestCharterCarriesTheManagersSignInRules(t *testing.T) {
	k := &Kit{}
	for _, want := range []string{
		"Security Key or Biometric Authenticator", "An Okta push happens only if the passkey click itself fails",
		"trigger it once and `ask` once", "Never trigger a push to answer a mere question",
		"matching on the sender and never on subject words",
	} {
		if !strings.Contains(k.Charter(), want) {
			t.Errorf("charter lacks %q", want)
		}
	}
}
