package browser

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	testUser     = "ankur@example.com"
	testPassword = "s3cret-pw-do-not-leak"
)

// fakeChaavi serves one login for example.test, with a passkey when pk is set.
func fakeChaavi(t *testing.T, pk *Passkey) *chaaviClient {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/items", func(w http.ResponseWriter, r *http.Request) {
		items := []map[string]any{}
		if r.URL.Query().Get("uri") == "example.test" && r.URL.Query().Get("type") == "login" {
			login := map[string]any{"username": testUser, "passkeys": []map[string]string{}}
			if pk != nil {
				login["passkeys"] = []map[string]string{{"rpId": pk.RPID}}
			}
			items = append(items, map[string]any{"id": "i1", "name": "Example", "type": "login", "login": login})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	})
	mux.HandleFunc("POST /v1/items/i1/login", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"username": testUser, "password": testPassword})
	})
	mux.HandleFunc("POST /v1/items/i1/passkey", func(w http.ResponseWriter, _ *http.Request) {
		if pk == nil {
			http.Error(w, `{"error":{"type":"not_found","message":"no passkey"}}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(pk)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &chaaviClient{base: srv.URL, http: &http.Client{Timeout: 10 * time.Second}}
}

const loginPage = `<form id="f1"><input name="username" aria-label="Email" autocomplete="username">
<label><input type="checkbox" id="rm"> Remember this device</label><button type="submit">Next</button></form>
<form id="f2" hidden><input type="password" name="password" aria-label="Password"><button type="submit">Sign in</button></form>
<form id="code" hidden><input name="otp" autocomplete="one-time-code" aria-label="Code"></form>
<p id="out"></p>
<script>
f1.onsubmit = e => { e.preventDefault(); window.user = f1.username.value; window.remember = rm.checked; f1.hidden = true; f2.hidden = false; };
f2.onsubmit = e => { e.preventDefault(); window.pw = f2.password.value;
  if (location.hash === '#code') { f2.hidden = true; code.hidden = false; out.textContent = 'Enter the 6-digit verification code we texted to ***3805'; }
  else out.textContent = 'Welcome back'; };
</script>`

func localhostURL(srv *httptest.Server) string {
	u, _ := url.Parse(srv.URL)
	return "http://localhost:" + u.Port()
}

// TestSignInFillsTheLoginWithoutShowingThePassword drives a two-step sign-in: the username is
// submitted on one step and the password on the next, "remember this device" is ticked, and
// the password reaches the page but not the result.
func TestSignInFillsTheLoginWithoutShowingThePassword(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	srv := serve(t, map[string]string{"/": loginPage})
	k := &Kit{chaavi: fakeChaavi(t, nil)}
	tab := openPage(t, ctx, b, localhostURL(srv)+"/")
	out, err := k.signIn(ctx, tab, input{Site: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, testPassword) || !strings.Contains(out, "Welcome back") {
		t.Fatalf("result:\n%s", out)
	}
	if jsIn(t, ctx, tab, "", `window.pw`) != testPassword || jsIn(t, ctx, tab, "", `window.user`) != testUser || jsIn(t, ctx, tab, "", `window.remember`) != true {
		t.Fatal("the login did not reach the page, or remember this device was not ticked")
	}
}

// TestSignInReturnsNeedsCodeWhenAskedForOne stops at a one-time code page and says where it
// was sent.
func TestSignInReturnsNeedsCodeWhenAskedForOne(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	srv := serve(t, map[string]string{"/": loginPage})
	k := &Kit{chaavi: fakeChaavi(t, nil)}
	tab := openPage(t, ctx, b, localhostURL(srv)+"/#code")
	out, err := k.signIn(ctx, tab, input{Site: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "needs_code:") || !strings.Contains(out, "***3805") || strings.Contains(out, testPassword) {
		t.Fatalf("result:\n%s", out)
	}
}

// TestSignInUsesThePasskeyFirstAndTheOktaKeyRow loads the passkey, reloads the page, and picks
// Okta's Security Key or Biometric Authenticator from the list.
func TestSignInUsesThePasskeyFirstAndTheOktaKeyRow(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	pk := newPasskey(t)
	srv := serve(t, map[string]string{"/": `<script>sessionStorage.loads = (+sessionStorage.loads || 0) + 1</script>
<h2>Verify it's you</h2>
<div class="row"><div>Okta Verify</div><a href="#" role="button" onclick="window.picked='verify'">Select</a></div>
<div class="row"><div>Security Key or Biometric Authenticator</div><a href="#" role="button" onclick="window.picked='key'">Select</a></div>`})
	k := &Kit{chaavi: fakeChaavi(t, &pk)}
	tab := openPage(t, ctx, b, localhostURL(srv)+"/")
	out, err := k.signIn(ctx, tab, input{Site: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "passkey for localhost loaded") || !strings.Contains(out, "chose Security Key or Biometric Authenticator") {
		t.Fatalf("result:\n%s", out)
	}
	if strings.Contains(out, pk.PrivateKey) {
		t.Fatal("the result carries the private key")
	}
	if jsIn(t, ctx, tab, "", `window.picked`) != "key" || jsIn(t, ctx, tab, "", `sessionStorage.loads`) != "2" {
		t.Fatal("the Okta key row was not chosen after a reload")
	}
	if _, err := k.signIn(ctx, tab, input{Site: "nowhere.test"}); err == nil || !strings.Contains(err.Error(), "no login for nowhere.test") {
		t.Fatalf("unknown site: %v", err)
	}
}
