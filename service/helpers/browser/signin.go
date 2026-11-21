package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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

// onlyPasskeyItem is the one item that has a passkey, or items when none or several do.
func onlyPasskeyItem(items []chaaviItem) []chaaviItem {
	var with []chaaviItem
	for _, it := range items {
		if it.Login != nil && len(it.Login.Passkeys) > 0 {
			with = append(with, it)
		}
	}
	if len(with) == 1 {
		return with
	}
	return items
}

// itemsForHost narrows items, found by the registrable domain, to those whose address is host or
// a parent domain of it, keeping the most specific address; Chaavi matches an address by
// substring, so it cannot do this itself. It returns items when none of them names host.
func itemsForHost(items []chaaviItem, host string) []chaaviItem {
	best := ""
	var matched []chaaviItem
	for _, it := range items {
		if it.Login == nil {
			continue
		}
		itemBest := ""
		for _, u := range it.Login.URIs {
			address := hostOf(u.URI)
			if (host == address || strings.HasSuffix(host, "."+address)) && len(address) > len(itemBest) {
				itemBest = address
			}
		}
		switch {
		case itemBest == "" || len(itemBest) < len(best):
		case len(itemBest) > len(best):
			best, matched = itemBest, []chaaviItem{it}
		default:
			matched = append(matched, it)
		}
	}
	if len(matched) == 0 {
		return items
	}
	return matched
}

// hostOf is the lowercase host of a URL or bare host name.
func hostOf(raw string) string {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
}

// chooseLogin is the one Chaavi login of site that in names: the item called in.Item, else, for
// a passkey sign-in (in.Use is not login), the only one with a passkey.
func chooseLogin(items []chaaviItem, in input, site string) (chaaviItem, error) {
	if in.Item != "" {
		var named []chaaviItem
		for _, it := range items {
			if strings.EqualFold(it.Name, in.Item) {
				named = append(named, it)
			}
		}
		items = named
	}
	if len(items) > 1 && in.Use != "login" {
		items = onlyPasskeyItem(items)
	}
	switch {
	case len(items) == 0:
		return chaaviItem{}, fmt.Errorf("Chaavi has no login for %s", site)
	case len(items) > 1:
		var names []string
		for _, it := range items {
			names = append(names, jsonString(it.Name))
		}
		return chaaviItem{}, fmt.Errorf("Chaavi has %d logins for %s (%s); pass item to choose one", len(items), site, strings.Join(names, ", "))
	}
	return items[0], nil
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
	host := hostOf(in.Site)
	if in.Site == "" {
		host = hostOf(tab.URL())
	}
	items = itemsForHost(items, host)
	item, err := chooseLogin(items, in, site)
	if err != nil {
		return "", err
	}
	hasPasskey := item.Login != nil && len(item.Login.Passkeys) > 0
	if hasPasskey && in.Use != "login" {
		return k.signInPasskey(ctx, tab, item)
	}
	if in.Use == "passkey" {
		return "", fmt.Errorf("the Chaavi login %s has no passkey", jsonString(item.Name))
	}
	return k.signInLogin(ctx, tab, item)
}

// signInPasskey loads the item's passkey into the tab, reloads it, and carries the sign-in
// through MSU's Okta/SAML route: the login page's single sign-on entry link, Okta's sign-in
// page with his address already filled (its Next), Okta's Security Key or Biometric
// Authenticator row, and its Next button. Each step runs only when the page shows it; the
// result is the page where the route stopped, signed in or blocked.
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
	keyRow, err := tab.refByScript(ctx, oktaKeyScript)
	if err != nil {
		return "", err
	}
	if keyRow == "" {
		entry, err := tab.refByScript(ctx, ssoLinkScript)
		if err != nil {
			return "", err
		}
		if entry != "" {
			if err := tab.Click(ctx, entry, false); err != nil {
				return "", fmt.Errorf("following the single sign-on link: %w", err)
			}
			time.Sleep(signInSettle)
			out += "; followed the single sign-on link"
		}
		step, ref, err := tab.routeStep(ctx, entry != "", oktaKeyScript, identifierNextScript)
		if err != nil {
			return "", err
		}
		switch step {
		case 0:
			keyRow = ref
		case 1:
			if err := tab.Click(ctx, ref, false); err != nil {
				return "", fmt.Errorf("pressing Next on the sign-in address: %w", err)
			}
			time.Sleep(signInSettle)
			out += "; pressed Next on the filled sign-in address"
			if keyRow, err = tab.refByScript(ctx, oktaKeyScript); err != nil {
				return "", err
			}
		}
	}
	if keyRow != "" {
		if err := tab.Click(ctx, keyRow, false); err != nil {
			return "", fmt.Errorf("selecting Security Key or Biometric Authenticator: %w", err)
		}
		time.Sleep(signInSettle)
		out += "; chose Security Key or Biometric Authenticator"
		next, err := tab.refByScript(ctx, nextButtonScript)
		if err != nil {
			return "", err
		}
		if next != "" {
			if err := tab.Click(ctx, next, false); err != nil {
				return "", fmt.Errorf("pressing Next: %w", err)
			}
			time.Sleep(signInSettle)
			out += "; pressed Next"
		}
	}
	page, err := tab.Look(ctx, "")
	if err != nil {
		return "", err
	}
	return out + "\nRead the page: it is signed in, or shows what blocks it, such as a passkey or security-key option to click.\n" + clipText(page, 3_000, "look for the rest"), nil
}

// routeStep is the index and ref of the first of scripts (ref-finding scripts, as refByScript
// runs them) that the page shows, or -1. With wait, a page that has not rendered a step yet is
// read again until renderWait has passed, as an Okta page is after the single sign-on link.
func (t *Tab) routeStep(ctx context.Context, wait bool, scripts ...string) (int, string, error) {
	deadline := time.Now().Add(renderWait)
	for {
		for i, script := range scripts {
			ref, err := t.refByScript(ctx, script)
			if err != nil {
				return -1, "", err
			}
			if ref != "" {
				return i, ref, nil
			}
		}
		if !wait || time.Now().After(deadline) {
			return -1, "", nil
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return -1, "", ctx.Err()
		}
	}
}

// refByScript is the ref script (one of the ref-finding scripts in scripts.go, evaluated as
// (script)(start)) names in the first frame that has one, or "".
func (t *Tab) refByScript(ctx context.Context, script string) (string, error) {
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
		if err := t.eval(ctx, f, fmt.Sprintf("(%s)(%d)", script, start), &got); err != nil {
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
