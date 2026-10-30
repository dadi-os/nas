package browser

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	// signInSettle is how long a page is given after a passkey reload or a submit before it
	// is read.
	signInSettle = 1500 * time.Millisecond
	// fieldWait is how long the next field of a two-step sign-in may take to appear.
	fieldWait = 10 * time.Second
)

var codePrompt = regexp.MustCompile(`(?i)(verification code|security code|one-time (pass)?code|enter (the |your )?(\d-?digit )?code|6-digit|6 digit|code we (sent|texted|emailed)|authenticator app|text message|sent (you )?a code|passcode)`)

type loginFields struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Code     string `json:"code"`
	Remember *struct {
		Ref     string `json:"ref"`
		Checked bool   `json:"checked"`
	} `json:"remember"`
	Next int `json:"next"`
}

// fields finds the sign-in fields in the first frame that has any.
func (t *Tab) fields(ctx context.Context) (loginFields, error) {
	frames, err := t.framesInOrder(ctx)
	if err != nil {
		return loginFields{}, err
	}
	for _, f := range frames {
		t.mu.Lock()
		start := t.nextRef + 1
		t.mu.Unlock()
		var got loginFields
		if err := t.eval(ctx, f, fmt.Sprintf("(%s)(%d)", loginFieldsScript, start), &got); err != nil {
			return loginFields{}, err
		}
		t.mu.Lock()
		if got.Next-1 > t.nextRef {
			t.nextRef = got.Next - 1
		}
		t.mu.Unlock()
		if got.Username != "" || got.Password != "" || got.Code != "" {
			return got, nil
		}
	}
	return loginFields{}, nil
}

// waitFields waits for the sign-in fields to satisfy done.
func (t *Tab) waitFields(ctx context.Context, done func(loginFields) bool) (loginFields, error) {
	deadline := time.Now().Add(fieldWait)
	for {
		got, err := t.fields(ctx)
		if err == nil && done(got) {
			return got, nil
		}
		if time.Now().After(deadline) {
			return got, errors.New("the sign-in page did not show the next field")
		}
		select {
		case <-time.After(250 * time.Millisecond):
		case <-ctx.Done():
			return got, ctx.Err()
		}
	}
}

// tickRemember ticks the remember-this-device checkbox when the page offers one.
func (t *Tab) tickRemember(ctx context.Context, f loginFields) error {
	if f.Remember != nil && !f.Remember.Checked {
		return t.Check(ctx, f.Remember.Ref, true)
	}
	return nil
}

// signIn signs the tab in with the Chaavi login for its site.
func (k *Kit) signIn(ctx context.Context, tab *Tab, in input) (string, error) {
	site := siteOf(in.Site)
	if in.Site == "" {
		site = siteOf(tab.URL())
	}
	if site == "" {
		return "", errors.New("the tab is on no site; navigate to the site's sign-in page first, or pass site")
	}
	items, err := k.chaavi.logins(site)
	if err != nil {
		return "", err
	}
	if in.Item != "" {
		var named []chaaviItem
		for _, it := range items {
			if strings.EqualFold(it.Name, in.Item) {
				named = append(named, it)
			}
		}
		items = named
	}
	switch {
	case len(items) == 0:
		return "", fmt.Errorf("Chaavi has no login for %s", site)
	case len(items) > 1:
		var names []string
		for _, it := range items {
			names = append(names, jsonString(it.Name))
		}
		return "", fmt.Errorf("Chaavi has %d logins for %s (%s); pass item to choose one", len(items), site, strings.Join(names, ", "))
	}
	item := items[0]
	hasPasskey := item.Login != nil && len(item.Login.Passkeys) > 0
	if hasPasskey && in.Use != "login" {
		return k.signInPasskey(ctx, tab, item)
	}
	if in.Use == "passkey" {
		return "", fmt.Errorf("the Chaavi login %s has no passkey", jsonString(item.Name))
	}
	return k.signInLogin(ctx, tab, item)
}

// signInPasskey loads the item's passkey into the tab, reloads it, and follows an Okta list
// to its Security Key or Biometric Authenticator.
func (k *Kit) signInPasskey(ctx context.Context, tab *Tab, item chaaviItem) (string, error) {
	key, err := k.chaavi.passkey(item.ID)
	if err != nil {
		return "", err
	}
	if err := tab.AddPasskey(ctx, key); err != nil {
		return "", err
	}
	time.Sleep(signInSettle)
	out := fmt.Sprintf("passkey for %s loaded and the page reloaded", key.RPID)
	if ref, err := tab.oktaKey(ctx); err != nil {
		return "", err
	} else if ref != "" {
		if err := tab.Click(ctx, ref, false); err != nil {
			return "", fmt.Errorf("selecting Security Key or Biometric Authenticator: %w", err)
		}
		time.Sleep(signInSettle)
		out += "; chose Security Key or Biometric Authenticator"
	}
	page, err := tab.Look(ctx, "")
	if err != nil {
		return "", err
	}
	return out + "\nRead the page: it is signed in, or shows a passkey or security-key option to click.\n" + clipText(page, 3_000, "look for the rest"), nil
}

// oktaKey is the ref of the Select control of an Okta list's Security Key or Biometric
// Authenticator row, or "".
func (t *Tab) oktaKey(ctx context.Context) (string, error) {
	frames, err := t.framesInOrder(ctx)
	if err != nil {
		return "", err
	}
	for _, f := range frames {
		t.mu.Lock()
		start := t.nextRef + 1
		t.mu.Unlock()
		var got struct {
			Ref  string `json:"ref"`
			Next int    `json:"next"`
		}
		if err := t.eval(ctx, f, fmt.Sprintf("(%s)(%d)", oktaKeyScript, start), &got); err != nil {
			return "", err
		}
		t.mu.Lock()
		if got.Next-1 > t.nextRef {
			t.nextRef = got.Next - 1
		}
		t.mu.Unlock()
		if got.Ref != "" {
			return got.Ref, nil
		}
	}
	return "", nil
}

// signInLogin fills the item's username and password into the page's sign-in form, one step
// at a time when the site asks for them on separate pages, ticks remember this device, and
// submits. The password goes from Chaavi into the page and no further.
func (k *Kit) signInLogin(ctx context.Context, tab *Tab, item chaaviItem) (string, error) {
	username, password, err := k.chaavi.credentials(item.ID)
	if err != nil {
		return "", err
	}
	out, err := k.fillLogin(ctx, tab, username, password)
	if password != "" {
		out = strings.ReplaceAll(out, password, "***")
	}
	if err != nil {
		return "", errors.New(strings.ReplaceAll(err.Error(), password, "***"))
	}
	return out, nil
}

func (k *Kit) fillLogin(ctx context.Context, tab *Tab, username, password string) (string, error) {
	f, err := tab.fields(ctx)
	if err != nil {
		return "", err
	}
	if f.Username == "" && f.Password == "" {
		return "", errors.New("the page shows no sign-in fields; navigate to the sign-in page")
	}
	if f.Password == "" {
		if username == "" {
			return "", errors.New("the page asks for a username and the Chaavi login has none")
		}
		if err := tab.tickRemember(ctx, f); err != nil {
			return "", err
		}
		if _, err := tab.Type(ctx, f.Username, username, true); err != nil {
			return "", err
		}
		if f, err = tab.waitFields(ctx, func(f loginFields) bool { return f.Password != "" || f.Code != "" }); err != nil {
			return "", err
		}
	} else if f.Username != "" && username != "" {
		if _, err := tab.Type(ctx, f.Username, username, false); err != nil {
			return "", err
		}
	}
	if f.Password != "" {
		if err := tab.tickRemember(ctx, f); err != nil {
			return "", err
		}
		if _, err := tab.Type(ctx, f.Password, password, true); err != nil {
			return "", err
		}
	}
	time.Sleep(signInSettle)
	return tab.afterSubmit(ctx)
}

// afterSubmit reads the page after a sign-in submit: needs_code when it asks for a one-time
// code, else its outline.
func (t *Tab) afterSubmit(ctx context.Context) (string, error) {
	f, err := t.fields(ctx)
	if err != nil {
		return "", err
	}
	body, err := t.pageText(ctx)
	if err != nil {
		return "", err
	}
	if f.Code != "" || (f.Password == "" && codePrompt.MatchString(body)) {
		hit := codePrompt.FindString(body)
		line := ""
		for _, l := range strings.Split(body, "\n") {
			if hit != "" && strings.Contains(strings.ToLower(l), strings.ToLower(hit)) {
				line = strings.TrimSpace(l)
				break
			}
		}
		ref := ""
		if f.Code != "" {
			ref = " code field " + f.Code + ";"
		}
		return fmt.Sprintf("needs_code:%s the page says %s. Ask for the code, then type it into the field and submit (and tick remember this device).", ref, jsonString(clipText(line, 200, ""))), nil
	}
	page, err := t.Look(ctx, "")
	if err != nil {
		return "", err
	}
	return "submitted the login. Read the page to confirm it signed in.\n" + clipText(page, 3_000, "look for the rest"), nil
}
