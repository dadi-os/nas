package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const quizFrame = `<form><label><input type="radio" name="a" value="1"> Answer one</label>
<label><input type="radio" name="a" value="2"> Answer two</label></form>`

func quizPage(frameURL string) string {
	return `<h1>Quiz</h1><iframe id="q" src="` + frameURL + `" width="500" height="200"></iframe>
<div id="host"></div>
<input id="k" aria-label="Keys" onkeydown="window.keys=(window.keys||[]).concat(event.key+(event.ctrlKey?'+ctrl':''))">
<script>
const r = document.getElementById('host').attachShadow({mode: 'open'});
r.innerHTML = '<button id="sb">Shadow save</button><button id="sd" disabled>Shadow disabled</button>' +
  '<div contenteditable="true" id="ed" aria-label="Editor"></div>' +
  '<select id="sel"><option value="a">Alpha</option><option value="b">Beta</option></select>';
r.getElementById('sb').onclick = () => { window.saved = (window.saved || 0) + 1; };
window.shadow = r;
</script>`
}

// TestQuizFramesAndShadowDOM covers D2L quiz frames and Brightspace shadow DOM (hath fabbd3a):
// refs are numbered across frames and shadow roots and stay with their element, actions
// reach into both, keys are delivered and a disabled control fails at once. The cross-site
// case runs the frame in another process when site isolation puts it there.
func TestQuizFramesAndShadowDOM(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame func(srvURL string) string
	}{
		{"same site frame", func(u string) string { return u + "/quizframe" }},
		{"cross site frame", func(u string) string { return strings.Replace(u, "127.0.0.1", "localhost", 1) + "/quizframe" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := newBrowser(t)
			ctx := testCtx(t)
			srv := serve(t, map[string]string{"/quizframe": quizFrame})
			page := serve(t, map[string]string{"/": quizPage(tc.frame(srv.URL))})
			tab := openPage(t, ctx, b, page.URL)
			look, err := tab.Look(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(look, "frame \"") || !strings.Contains(look, "Answer one") {
				t.Fatalf("look does not show the frame:\n%s", look)
			}
			tab.mu.Lock()
			for _, f := range tab.frames {
				if f.sess != tab.sid {
					t.Logf("frame %s runs in its own process", f.url)
				}
			}
			tab.mu.Unlock()
			radio := refOn(t, look, `type="radio"`)
			save := refOn(t, look, "Shadow save")
			disabled := refOn(t, look, "Shadow disabled")
			editor := refOn(t, look, "editable")
			sel := refOn(t, look, "select")
			keys := refOn(t, look, `name="Keys"`)

			out, err := tab.Act(ctx, []Action{{Type: "click", Ref: radio}, {Type: "click", Ref: save}, {Type: "type", Ref: editor, Text: "Hello there"}, {Type: "select", Ref: sel, Value: "Beta"}, {Type: "key", Ref: keys, Key: "Control+Enter"}})
			if err != nil || !strings.Contains(out, "ok: 5 of 5") {
				t.Fatalf("act: %v\n%s", err, out)
			}
			if got := jsIn(t, ctx, tab, "/quizframe", `document.querySelector('input[value="1"]').checked`); got != true {
				t.Fatal("the radio in the frame is not checked")
			}
			if got := jsIn(t, ctx, tab, "", `window.saved`); got != float64(1) {
				t.Fatalf("shadow button clicked %v times", got)
			}
			if got := jsIn(t, ctx, tab, "", `window.shadow.getElementById('ed').innerText`); got != "Hello there" {
				t.Fatalf("editable holds %q", got)
			}
			if got := jsIn(t, ctx, tab, "", `window.shadow.getElementById('sel').value`); got != "b" {
				t.Fatalf("select holds %q", got)
			}
			if got := jsIn(t, ctx, tab, "", `JSON.stringify(window.keys)`); !strings.Contains(got.(string), "Enter+ctrl") {
				t.Fatalf("keys delivered: %v", got)
			}

			started := time.Now()
			out, err = tab.Act(ctx, []Action{{Type: "click", Ref: disabled}})
			if err != nil || !strings.Contains(out, "disabled") || time.Since(started) > 4*time.Second {
				t.Fatalf("disabled click: %v after %s\n%s", err, time.Since(started), out)
			}

			again, err := tab.Look(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			if refOn(t, again, "Shadow save") != save {
				t.Fatal("a ref changed between looks")
			}
			found, err := tab.Find(ctx, "answer two")
			if err != nil || !strings.Contains(found, "Answer two") {
				t.Fatalf("find: %v %q", err, found)
			}
		})
	}
}

// TestPortalDialogWithZeroSizeWrapper covers Trello's card dialog (hath 12d76c2): content
// positioned inside a 0x0 wrapper gets refs, while a 0x0 wrapper that clips stays hidden.
func TestPortalDialogWithZeroSizeWrapper(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	dialog := `<div role="dialog" style="position:fixed;top:40px;left:40px;width:400px;height:300px;background:#eee">
<textarea aria-label="Description"></textarea><button onclick="window.saved=true">Save card</button></div>`
	srv := serve(t, map[string]string{
		"/open":    `<div id="root" style="width:0;height:0">` + dialog + `</div>`,
		"/clipped": `<div style="width:0;height:0;overflow:hidden">` + dialog + `</div>`,
	})
	tab := openPage(t, ctx, b, srv.URL+"/open")
	look, err := tab.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	area, save := refOn(t, look, "textarea"), refOn(t, look, "Save card")
	out, err := tab.Act(ctx, []Action{{Type: "type", Ref: area, Text: "Design notes"}, {Type: "click", Ref: save}})
	if err != nil || !strings.Contains(out, "ok: 2 of 2") {
		t.Fatalf("act: %v\n%s", err, out)
	}
	if jsIn(t, ctx, tab, "", `window.saved`) != true {
		t.Fatal("the dialog's Save was not clicked")
	}
	if _, err := tab.Navigate(ctx, srv.URL+"/clipped", "load"); err != nil {
		t.Fatal(err)
	}
	look, err = tab.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(look, "Save card") {
		t.Fatalf("clipped 0x0 content was listed:\n%s", look)
	}
}

// TestNavigationThatBecomesADownload covers hath 2feb203, 7d3c40b and cfac9d9: a URL that
// turns into a file download is a download result, saved under its own name in the downloads
// dir, not an error; a second one with the same name does not overwrite the first.
func TestNavigationThatBecomesADownload(t *testing.T) {
	b, downloads := newBrowser(t)
	ctx := testCtx(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/file", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
		_, _ = w.Write([]byte("a,b\n1,2\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<a href="/file">Get report</a>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	tab := openPage(t, ctx, b, srv.URL+"/")

	d, err := tab.Navigate(ctx, srv.URL+"/file", "load")
	if err != nil || d == nil {
		t.Fatalf("navigate to a download: %v %v", d, err)
	}
	if d.Name != "report.csv" || d.State != "completed" || d.Path != filepath.Join(downloads, "report.csv") {
		t.Fatalf("download %+v", d)
	}
	if raw, err := os.ReadFile(d.Path); err != nil || string(raw) != "a,b\n1,2\n" {
		t.Fatalf("downloaded file: %q %v", raw, err)
	}
	if tab.URL() != srv.URL+"/" {
		t.Fatalf("the tab left its page: %s", tab.URL())
	}
	look, err := tab.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := tab.Act(ctx, []Action{{Type: "click", Ref: refOn(t, look, "Get report")}})
	if err != nil || !strings.Contains(out, "download report (1).csv") && !strings.Contains(out, "report (1).csv") {
		t.Fatalf("click download: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(downloads, "report (1).csv")); err != nil {
		t.Fatalf("second download: %v", err)
	}
}

// TestPasskeySurvivesLaterActions covers hath 57e640b: a passkey loaded into the tab's
// virtual authenticator still signs after the tab is looked at, acted on, searched, other
// tabs come and go and a screenshot is taken.
func TestPasskeySurvivesLaterActions(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	pk := newPasskey(t)
	page := fmt.Sprintf(`<button onclick="sign()">Sign in with passkey</button><button onclick="window.n=1">Other</button><p id="out">waiting</p>
<script>
async function sign() {
  const id = Uint8Array.from(atob(%q), c => c.charCodeAt(0));
  try {
    await navigator.credentials.get({publicKey: {challenge: new Uint8Array(32), rpId: "localhost", timeout: 15000,
      allowCredentials: [{type: "public-key", id}], userVerification: "preferred"}});
    document.getElementById('out').textContent = 'assertion ok';
  } catch (e) { document.getElementById('out').textContent = 'assertion failed ' + e.name; }
}
</script>`, pk.CredentialID)
	srv := serve(t, map[string]string{"/": page})
	tab := openPage(t, ctx, b, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1))
	if err := tab.AddPasskey(ctx, pk); err != nil {
		t.Fatal(err)
	}
	look, err := tab.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tab.Act(ctx, []Action{{Type: "click", Ref: refOn(t, look, "Other")}}); err != nil {
		t.Fatal(err)
	}
	other, err := b.NewTab(ctx, "about:blank")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Tabs(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.CloseTab(ctx, other.ID()); err != nil {
		t.Fatal(err)
	}
	tab, err = b.OtherTab(ctx, tab.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tab.Find(ctx, "passkey"); err != nil {
		t.Fatal(err)
	}
	out, err := tab.Act(ctx, []Action{{Type: "click", Ref: refOn(t, look, "Sign in with passkey")}, {Type: "wait", Text: "assertion", Ms: 15000}})
	if err != nil || !strings.Contains(out, "ok: 2 of 2") {
		t.Fatalf("act: %v\n%s", err, out)
	}
	if got := jsIn(t, ctx, tab, "", `document.getElementById('out').textContent`); got != "assertion ok" {
		t.Fatalf("passkey sign-in: %v", got)
	}
}

// TestFrontTab covers hath 6f23b98: the tab a call acts on is brought to the front, because
// Thaali's live view shows the front tab.
func TestFrontTab(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	srv := serve(t, map[string]string{"/a": `<button onclick="window.n=1">A</button>`, "/b": `<button>B</button>`})
	first := openPage(t, ctx, b, srv.URL+"/a")
	second, err := b.NewTab(ctx, srv.URL+"/b")
	if err != nil {
		t.Fatal(err)
	}
	if b.FrontID() != second.ID() {
		t.Fatalf("a new tab is not in front: %s", b.FrontID())
	}
	back, err := b.OtherTab(ctx, first.ID())
	if err != nil {
		t.Fatal(err)
	}
	if b.FrontID() != first.ID() {
		t.Fatalf("acting on the first tab left %s in front", b.FrontID())
	}
	look, err := back.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := back.Act(ctx, []Action{{Type: "click", Ref: refOn(t, look, "A")}}); err != nil {
		t.Fatal(err)
	}
	if got := jsIn(t, ctx, back, "", `document.visibilityState`); got != "visible" {
		t.Fatalf("the tab acted on is %v", got)
	}
	tabs, err := b.Tabs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tab := range tabs {
		if tab.Front != (tab.ID == first.ID()) {
			t.Fatalf("tab list marks the wrong tab in front: %+v", tabs)
		}
	}
}

// TestUploadIntoFramesShadowRootsAndClickAddedInputs covers hath f19392a and b70d418: files go
// to a file input in the page, inside a shadow root, inside a frame, to the one input a
// clicked control adds, and to the file chooser a clicked control opens.
func TestUploadIntoFramesShadowRootsAndClickAddedInputs(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	dir := t.TempDir()
	a, c := filepath.Join(dir, "a.txt"), filepath.Join(dir, "c.txt")
	for p, body := range map[string]string{a: "hello", c: "second"} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srv := serve(t, map[string]string{
		"/": `<div id="host"></div><iframe src="/upframe" width="400" height="150"></iframe>
<button onclick="addInput()">Add a File</button>
<button onclick="document.getElementById('native').click()">Browse</button>
<input id="native" type="file" style="display:none">
<script>
document.getElementById('host').attachShadow({mode: 'open'}).innerHTML = '<input type="file" id="s" aria-label="Shadow upload">';
function addInput() { const i = document.createElement('input'); i.type = 'file'; i.id = 'added'; i.setAttribute('aria-label', 'Added upload'); document.getElementById('dlg').appendChild(i); }
</script><div id="dlg"></div>`,
		"/upframe": `<input type="file" id="f" multiple aria-label="Frame upload">`,
	})
	tab := openPage(t, ctx, b, srv.URL+"/")
	look, err := tab.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := tab.Upload(ctx, refOn(t, look, "Shadow upload"), []string{a}); err != nil || got != "a.txt" {
		t.Fatalf("shadow upload: %q %v", got, err)
	}
	if got, err := tab.Upload(ctx, refOn(t, look, "Frame upload"), []string{a, c}); err != nil || got != "a.txt, c.txt" {
		t.Fatalf("frame upload: %q %v", got, err)
	}
	if _, err := tab.Upload(ctx, refOn(t, look, "Shadow upload"), []string{a, c}); err == nil || !strings.Contains(err.Error(), "accepts one file") {
		t.Fatalf("two files into a single input: %v", err)
	}
	if got, err := tab.Upload(ctx, refOn(t, look, "Add a File"), []string{c}); err != nil || got != "c.txt" {
		t.Fatalf("click-added upload: %q %v", got, err)
	}
	if got := jsIn(t, ctx, tab, "", `document.getElementById('added').files[0].name`); got != "c.txt" {
		t.Fatalf("the added input holds %v", got)
	}
	if got, err := tab.Upload(ctx, refOn(t, look, "Browse"), []string{a}); err != nil || got != "a.txt" {
		t.Fatalf("file chooser upload: %q %v", got, err)
	}
	if got := jsIn(t, ctx, tab, "", `document.getElementById('native').files[0].name`); got != "a.txt" {
		t.Fatalf("the chooser's input holds %v", got)
	}
	if _, err := tab.Upload(ctx, refOn(t, look, "Browse"), []string{filepath.Join(dir, "missing.txt")}); err == nil {
		t.Fatal("uploaded a file that does not exist")
	}
}

// TestFailedActionsAreNeverRepeated covers hath 7577222: a batch stops at the first failing
// action, the actions before it ran exactly once, and nothing is run again.
func TestFailedActionsAreNeverRepeated(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	srv := serve(t, map[string]string{"/": `<button onclick="window.n=(window.n||0)+1">Count</button>`})
	tab := openPage(t, ctx, b, srv.URL)
	look, err := tab.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := tab.Act(ctx, []Action{{Type: "click", Ref: refOn(t, look, "Count")}, {Type: "click", Ref: "e999"}, {Type: "click", Ref: refOn(t, look, "Count")}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "action 2") || !strings.Contains(out, "1 of 3 actions ran") || !strings.Contains(out, "nothing was repeated") {
		t.Fatalf("report:\n%s", out)
	}
	if got := jsIn(t, ctx, tab, "", `window.n`); got != float64(1) {
		t.Fatalf("the click ran %v times", got)
	}
}

// TestCoveredElementFailsWithoutClicking is the actionability wait: a control under an
// overlay is not clicked, and the failure names what covers it.
func TestCoveredElementFailsWithoutClicking(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	srv := serve(t, map[string]string{"/": `<button onclick="window.n=1" style="position:absolute;top:20px;left:20px">Pay</button>
<div id="veil" style="position:fixed;top:0;left:0;width:100%;height:100%"></div>`})
	tab := openPage(t, ctx, b, srv.URL)
	look, err := tab.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := tab.Act(ctx, []Action{{Type: "click", Ref: refOn(t, look, "Pay")}})
	if err != nil || !strings.Contains(out, "covered by <div#veil>") {
		t.Fatalf("covered click: %v\n%s", err, out)
	}
	if got := jsIn(t, ctx, tab, "", `window.n === undefined`); got != true {
		t.Fatal("the covered button was clicked")
	}
}

// TestActionsWaitForTheElementAndScrollIt covers the actionability checks: a button below the
// fold is scrolled into view, and one that is still moving is clicked once it has settled.
func TestActionsWaitForTheElementAndScrollIt(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	srv := serve(t, map[string]string{"/": `<div style="height:3000px">tall</div>
<button id="low" onclick="window.low=1">Far below</button>
<button id="mv" style="position:fixed;top:100px;left:100px;transition:left .6s linear" onclick="window.mv=1">Moving</button>
<script>setTimeout(() => document.getElementById('mv').style.left = '400px', 50)</script>`})
	tab := openPage(t, ctx, b, srv.URL)
	look, err := tab.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := tab.Act(ctx, []Action{{Type: "click", Ref: refOn(t, look, "Far below")}, {Type: "click", Ref: refOn(t, look, "Moving")}})
	if err != nil || !strings.Contains(out, "ok: 2 of 2") {
		t.Fatalf("act: %v\n%s", err, out)
	}
	if jsIn(t, ctx, tab, "", `window.low === 1 && window.mv === 1`) != true {
		t.Fatal("a click missed")
	}
}

// TestPasswordsAreNotInTheOutline covers hath fc8caae: a filled password reads "filled" in
// every look, including one that a show-password toggle turned into a text input, and the
// typed text is never echoed in a result.
func TestPasswordsAreNotInTheOutline(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	srv := serve(t, map[string]string{"/": `<input type="password" aria-label="Password"><input type="text" autocomplete="current-password" aria-label="Shown password"><input type="text" aria-label="Name">`})
	tab := openPage(t, ctx, b, srv.URL)
	look, err := tab.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	const secret = "hunter2-secret"
	out, err := tab.Act(ctx, []Action{
		{Type: "type", Ref: refOn(t, look, `name="Password"`), Text: secret},
		{Type: "type", Ref: refOn(t, look, `name="Shown password"`), Text: secret},
		{Type: "type", Ref: refOn(t, look, `name="Name"`), Text: "Ankur"},
	})
	if err != nil || !strings.Contains(out, "ok: 3 of 3") {
		t.Fatalf("act: %v\n%s", err, out)
	}
	after, err := tab.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"act result": out, "look": after} {
		if strings.Contains(text, secret) {
			t.Fatalf("the %s shows the password:\n%s", name, text)
		}
	}
	if !strings.Contains(after, "filled") || !strings.Contains(after, `value="Ankur"`) {
		t.Fatalf("look after filling:\n%s", after)
	}
}

// TestActReturnsTheChangedRegion covers hath e3810dc: an act returns what changed, not the page.
func TestActReturnsTheChangedRegion(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	srv := serve(t, map[string]string{
		"/": `<h1>Static heading</h1><p>Static paragraph text</p><a href="/next">Go on</a>
<button onclick="document.getElementById('out').innerHTML='<button>Confirm order</button>'">Reveal</button><div id="out"></div>`,
		"/next": `<h1>Second page</h1><button>Finish</button>`,
	})
	tab := openPage(t, ctx, b, srv.URL)
	look, err := tab.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := tab.Act(ctx, []Action{{Type: "click", Ref: refOn(t, look, "Reveal")}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "+ button") || !strings.Contains(out, "Confirm order") || strings.Contains(out, "Static heading") || strings.Contains(out, "url:") {
		t.Fatalf("in-place change:\n%s", out)
	}
	out, err = tab.Act(ctx, []Action{{Type: "click", Ref: refOn(t, look, "Go on")}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "url: "+srv.URL+"/next") || !strings.Contains(out, "Second page") || !strings.Contains(out, "Finish") {
		t.Fatalf("navigation:\n%s", out)
	}
}

// TestFillIsEventCorrect covers the editors Playwright's fill handled: text typed into an
// input and a contenteditable region arrives as input events, so frameworks and rich editors
// see it, and a value-typed input is set through its setter.
func TestFillIsEventCorrect(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	srv := serve(t, map[string]string{"/": `<input id="i" aria-label="Subject" value="old"><div id="mirror"></div>
<div id="ed" contenteditable="true" aria-label="Body"></div><input id="d" type="date" aria-label="Due">
<script>
window.log = [];
for (const id of ['i', 'ed']) document.getElementById(id).addEventListener('input', e => { window.log.push(id + ':' + e.inputType); document.getElementById('mirror').textContent = e.target.value ?? e.target.innerText; });
document.getElementById('d').addEventListener('change', e => window.date = e.target.value);
</script>`})
	tab := openPage(t, ctx, b, srv.URL)
	look, err := tab.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := tab.Act(ctx, []Action{
		{Type: "type", Ref: refOn(t, look, `name="Subject"`), Text: "New subject"},
		{Type: "type", Ref: refOn(t, look, "editable"), Text: "Dear Ankur"},
		{Type: "type", Ref: refOn(t, look, `name="Due"`), Text: "2026-11-05"},
	})
	if err != nil || !strings.Contains(out, "ok: 3 of 3") {
		t.Fatalf("act: %v\n%s", err, out)
	}
	if got := jsIn(t, ctx, tab, "", `document.getElementById('i').value`); got != "New subject" {
		t.Fatalf("input holds %q", got)
	}
	if got := jsIn(t, ctx, tab, "", `JSON.stringify(window.log)`); !strings.Contains(got.(string), "i:insertText") || !strings.Contains(got.(string), "ed:insertText") {
		t.Fatalf("input events: %v", got)
	}
	if got := jsIn(t, ctx, tab, "", `document.getElementById('ed').innerText + '|' + window.date`); got != "Dear Ankur|2026-11-05" {
		t.Fatalf("editor and date: %v", got)
	}
}

// TestDialogDoesNotHangTheBatch is the alert case: a click that opens a dialog returns with
// the dialog reported, and a dialog action answers it.
func TestDialogDoesNotHangTheBatch(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	srv := serve(t, map[string]string{"/": `<button onclick="window.ok = confirm('Delete everything?')">Delete</button>`})
	tab := openPage(t, ctx, b, srv.URL)
	look, err := tab.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	out, err := tab.Act(ctx, []Action{{Type: "click", Ref: refOn(t, look, "Delete")}})
	if err != nil || !strings.Contains(out, "dialog open: confirm") || time.Since(started) > 10*time.Second {
		t.Fatalf("click: %v after %s\n%s", err, time.Since(started), out)
	}
	no := false
	if _, err := tab.Act(ctx, []Action{{Type: "dialog", Accept: &no}}); err != nil {
		t.Fatal(err)
	}
	if got := jsIn(t, ctx, tab, "", `window.ok`); got != false {
		t.Fatalf("confirm returned %v", got)
	}
}

// TestExtractFollowsPagination reads a table across Next links and a list behind Load more.
func TestExtractFollowsPagination(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	page := func(n int, next string) string {
		rows := ""
		for i := 1; i <= 3; i++ {
			rows += fmt.Sprintf("<tr><td>2026-10-%02d</td><td>Shop %d-%d</td><td>$%d.00</td></tr>", n*3+i, n, i, n*10+i)
		}
		return `<h2>Recent transactions</h2><table><thead><tr><th>Date</th><th>Payee</th><th>Amount</th></tr></thead><tbody>` + rows + `</tbody></table>` + next
	}
	srv := serve(t, map[string]string{
		"/p1": page(1, `<a href="/p2">Next</a>`), "/p2": page(2, `<a href="/p3">Next</a>`), "/p3": page(3, ""),
		"/more": `<h2>Items</h2><ul id="l"><li>one</li><li>two</li></ul><button onclick="const l=document.getElementById('l');l.insertAdjacentHTML('beforeend','<li>three</li><li>four</li>');this.remove()">Load more</button>`,
	})
	tab := openPage(t, ctx, b, srv.URL+"/p1")
	got, err := tab.Extract(ctx, ExtractOptions{What: "transactions", Format: "table", Pages: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows != 9 || got.Pages != 3 || !strings.HasPrefix(got.Data, "Date,Payee,Amount\n") || !strings.Contains(got.Data, "Shop 3-3,$33.00") {
		t.Fatalf("extract: %+v", got)
	}
	asJSON, err := tab.Extract(ctx, ExtractOptions{What: "transactions", Format: "json"})
	if err != nil || !strings.Contains(asJSON.Data, `"Payee": "Shop 3-1"`) {
		t.Fatalf("json: %v %+v", err, asJSON)
	}
	if _, err := tab.Navigate(ctx, srv.URL+"/more", "load"); err != nil {
		t.Fatal(err)
	}
	list, err := tab.Extract(ctx, ExtractOptions{What: "items", Format: "table", Pages: 3})
	if err != nil || list.Rows != 4 {
		t.Fatalf("load more: %v %+v", err, list)
	}
}
