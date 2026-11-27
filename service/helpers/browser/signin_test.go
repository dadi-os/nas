package browser

import (
	"encoding/json"
	"github.com/dadi-os/nas/service/helpers/vault"
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
func fakeChaavi(t *testing.T, pk *Passkey) *vault.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/items", func(w http.ResponseWriter, r *http.Request) {
		items := []map[string]any{}
		if r.URL.Query().Get("uri") == "example.test" && r.URL.Query().Get("type") == "login" {
			login := map[string]any{"username": testUser, "passkeys": []map[string]string{}, "hasPassword": true}
			if pk != nil {
				login["passkeys"] = []map[string]string{{"rpId": pk.RPID}}
			}
			items = append(items, map[string]any{"id": "i1", "name": "Example", "type": "login", "login": login})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	})
	mux.HandleFunc("POST /v1/items/i1/reveal", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"value": testPassword})
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
	return &vault.Client{Base: srv.URL, HTTP: &http.Client{Timeout: 10 * time.Second}}
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

// TestSignInFollowsTheSingleSignOnLinkAndPressesNext starts on a login page whose only way on is
// its single sign-on link, follows it to an Okta list, chooses the Security Key or Biometric row
// and presses Next, in one call.
func TestSignInFollowsTheSingleSignOnLinkAndPressesNext(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	pk := newPasskey(t)
	srv := serve(t, map[string]string{
		"/": `<h2>Login</h2><a href="/okta">Sign in with MSU NetID</a><a href="/help">Help</a>`,
		"/okta": `<h2>Verify it's you</h2>
<div class="row"><div>Security Key or Biometric Authenticator</div><a href="#" role="button" onclick="window.picked='key'">Select</a></div>
<button onclick="window.nexted = true">Next</button>`,
	})
	k := &Kit{chaavi: fakeChaavi(t, &pk)}
	tab := openPage(t, ctx, b, localhostURL(srv)+"/")
	out, err := k.signIn(ctx, tab, input{Site: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"followed the single sign-on link", "chose Security Key or Biometric Authenticator", "pressed Next"} {
		if !strings.Contains(out, step) {
			t.Fatalf("missing %q in:\n%s", step, out)
		}
	}
	if !strings.Contains(tab.URL(), "/okta") || jsIn(t, ctx, tab, "", `window.picked`) != "key" || jsIn(t, ctx, tab, "", `String(window.nexted)`) != "true" {
		t.Fatalf("the route did not reach the Okta list: %s", tab.URL())
	}
}

// TestSignInPressesNextOnOktasFilledAddress follows the single sign-on link to an Okta sign-in
// page that renders after its load event with his address already filled, as auth.msu.edu
// does for D2L, presses its Next, and reaches the Security Key row, in one call.
func TestSignInPressesNextOnOktasFilledAddress(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	pk := newPasskey(t)
	srv := serve(t, map[string]string{
		"/": `<h2>Login</h2><a href="/signin">Sign in with MSU NetID</a>`,
		"/signin": `<main id="m"></main><script>setTimeout(() => { document.getElementById('m').innerHTML =
  '<h1>Sign In</h1><input type="text" name="identifier" value="someone@msu.edu"><button onclick="location.href=\'/okta\'">Next</button>'; }, 1000)</script>`,
		"/okta": `<h2>Verify it's you</h2>
<div class="row"><div>Security Key or Biometric Authenticator</div><a href="#" role="button" onclick="window.picked='key'">Select</a></div>`,
	})
	k := &Kit{chaavi: fakeChaavi(t, &pk)}
	tab := openPage(t, ctx, b, localhostURL(srv)+"/")
	out, err := k.signIn(ctx, tab, input{Site: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"followed the single sign-on link", "pressed Next on the filled sign-in address", "chose Security Key or Biometric Authenticator"} {
		if !strings.Contains(out, step) {
			t.Fatalf("missing %q in:\n%s", step, out)
		}
	}
	if !strings.Contains(tab.URL(), "/okta") || jsIn(t, ctx, tab, "", `window.picked`) != "key" {
		t.Fatalf("the route did not reach the Okta list: %s", tab.URL())
	}
}

const testTotp = "424242"

// fakeChaaviTotp serves one login for example.test that has an authenticator key.
func fakeChaaviTotp(t *testing.T) *vault.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/items", func(w http.ResponseWriter, _ *http.Request) {
		login := map[string]any{"username": testUser, "passkeys": []map[string]string{}, "hasPassword": true, "hasTotp": true}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": "i1", "name": "Example", "type": "login", "login": login}}})
	})
	mux.HandleFunc("POST /v1/items/i1/reveal", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"value": testPassword})
	})
	mux.HandleFunc("POST /v1/items/i1/totp", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": testTotp, "period": 30, "remaining": 20})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &vault.Client{Base: srv.URL, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

const totpPage = `<form id="code"><input name="otp" autocomplete="one-time-code" aria-label="Code"><button type="submit">Verify</button></form>
<p id="ask">Enter the 6-digit code from your authenticator app</p><p id="out"></p>
<script>code.onsubmit = e => { e.preventDefault(); window.sent = code.otp.value; code.hidden = true; ask.hidden = true; out.textContent = 'Verified'; };</script>`

// TestSignInTotpFillsTheAuthenticatorCodeWithoutShowingIt types the code Chaavi makes into the
// page's code field; the code reaches the page but not the result.
func TestSignInTotpFillsTheAuthenticatorCodeWithoutShowingIt(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	srv := serve(t, map[string]string{"/": totpPage})
	k := &Kit{chaavi: fakeChaaviTotp(t)}
	tab := openPage(t, ctx, b, localhostURL(srv)+"/")
	out, err := k.signIn(ctx, tab, input{Site: "example.test", Use: "totp"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, testTotp) || !strings.Contains(out, "Verified") {
		t.Fatalf("result:\n%s", out)
	}
	if jsIn(t, ctx, tab, "", `window.sent`) != testTotp {
		t.Fatal("the code did not reach the page")
	}
}

const (
	testCardNumber = "4242424242424242"
	testCardCode   = "737"
)

// fakeChaaviCard serves a card and an identity.
func fakeChaaviCard(t *testing.T) *vault.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/items", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
 {"id":"c1","name":"Visa","type":"card","fields":[],"card":{"cardholderName":"Ankur Desai","brand":"Visa","expMonth":"07","expYear":"2029","hasNumber":true,"hasCode":true}},
 {"id":"i1","name":"Home","type":"identity","fields":[],"identity":{"firstName":"Ankur","lastName":"Desai","address1":"1 Main St","city":"Lansing","postalCode":"48823","hasSsn":true}}]}`))
	})
	mux.HandleFunc("POST /v1/items/{id}/reveal", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]string{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]string{"value": map[string]string{"number": testCardNumber, "code": testCardCode, "ssn": "078-05-1120"}[body["field"]]})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return vault.New(srv.URL)
}

const cardPage = `<form><input name="cardnum" autocomplete="cc-number"><input name="holder" autocomplete="cc-name">
<select name="m" autocomplete="cc-exp-month">` + `<option value="">Month</option><option value="1">1</option><option value="7">7</option><option value="12">12</option></select>
<select name="y" autocomplete="cc-exp-year"><option value="">Year</option><option value="29">2029</option><option value="30">2030</option></select>
<input name="cvv" aria-label="CVV"><input name="promo" aria-label="Promo"><input aria-label="Social"></form>`

// TestVaultFillAndTypePutSecretsInThePageNotInTheResult fills a card form, leaves the promo
// field alone and names it, types an identity secret into one ref, and shows no secret in
// any result or outline.
func TestVaultFillAndTypePutSecretsInThePageNotInTheResult(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	srv := serve(t, map[string]string{"/": cardPage})
	k := &Kit{chaavi: fakeChaaviCard(t)}
	s := &session{}
	tab := openPage(t, ctx, b, localhostURL(srv)+"/")
	out, err := k.vaultFill(ctx, s, tab, input{Item: "Visa"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, testCardNumber) || strings.Contains(out, testCardCode) || !strings.Contains(out, "left alone") || !strings.Contains(out, "promo") {
		t.Fatalf("result:\n%s", out)
	}
	read := `Array.from(document.forms[0].elements).map((e) => e.value).join("|")`
	if got := jsIn(t, ctx, tab, "", read); got != testCardNumber+"|Ankur Desai|7|29|"+testCardCode+"||" {
		t.Fatalf("the form holds %v", got)
	}
	look, err := tab.Look(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(look, testCardNumber) || strings.Contains(look, testCardCode) || !strings.Contains(look, "filled") {
		t.Fatalf("the outline shows a secret:\n%s", look)
	}
	social, _ := jsIn(t, ctx, tab, "", `document.querySelector('[aria-label="Social"]').getAttribute("data-dadi-ref")`).(string)
	if _, err := k.vaultType(ctx, s, tab, input{Item: "Home", Field: "ssn", Ref: social}); err != nil {
		t.Fatal(err)
	}
	if got := jsIn(t, ctx, tab, "", `document.querySelector('[aria-label="Social"]').value`); got != "078-05-1120" {
		t.Fatalf("the social field holds %v", got)
	}
	if scrubbed := vault.Scrub("number "+testCardNumber+" ssn 078-05-1120", s.secrets); strings.Contains(scrubbed, "4242") || strings.Contains(scrubbed, "1120") {
		t.Fatalf("the session does not scrub what it put in the page: %q", scrubbed)
	}
}

// TestSignInFillsTheAuthenticatorCodeAfterThePassword runs the whole login: username, password,
// and the authenticator code the page then asks for, none of them in the result.
func TestSignInFillsTheAuthenticatorCodeAfterThePassword(t *testing.T) {
	b, _ := newBrowser(t)
	ctx := testCtx(t)
	page := strings.Replace(loginPage, "Enter the 6-digit verification code we texted to ***3805", "Enter the 6-digit code from your authenticator app", 1)
	page = strings.Replace(page, "</script>", "code.onsubmit = e => { e.preventDefault(); window.sent = code.otp.value; code.hidden = true; out.textContent = 'Welcome back'; };</script>", 1)
	srv := serve(t, map[string]string{"/": page})
	k := &Kit{chaavi: fakeChaaviTotp(t)}
	tab := openPage(t, ctx, b, localhostURL(srv)+"/#code")
	out, err := k.signIn(ctx, tab, input{Site: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, testTotp) || strings.Contains(out, testPassword) || !strings.Contains(out, "Welcome back") {
		t.Fatalf("result:\n%s", out)
	}
	if jsIn(t, ctx, tab, "", `window.sent`) != testTotp || jsIn(t, ctx, tab, "", `window.pw`) != testPassword {
		t.Fatal("the password or the code did not reach the page")
	}
}
