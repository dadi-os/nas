package browser

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/dadi-os/nas/service/helpers/vault"
)

// maxVaultItems bounds what vault_find lists.
const maxVaultItems = 40

// formField is a control of the page's form that vault_fill may fill.
type formField struct {
	Ref    string `json:"ref"`
	Select bool   `json:"select"`
	Type   string `json:"type"`
	Auto   string `json:"auto"`
	Hint   string `json:"hint"`
}

// tokenFields are the Chaavi fields that can fill each autocomplete token, the first one the
// item has winning.
var tokenFields = map[string][]string{
	"cc-name": {"cardholderName"}, "cc-number": {"number"}, "cc-exp-month": {"expMonth"},
	"cc-exp-year": {"expYear"}, "cc-csc": {"code"}, "cc-type": {"brand"},
	"given-name": {"firstName", "givenName"}, "additional-name": {"middleName"},
	"family-name": {"lastName", "surname"}, "honorific-prefix": {"title"},
	"email": {"email"}, "tel": {"phone", "bankContactPhone"}, "organization": {"company"},
	"address-line1": {"address1"}, "address-line2": {"address2"}, "address-line3": {"address3"},
	"address-level2": {"city"}, "address-level1": {"state"}, "postal-code": {"postalCode"},
	"country": {"country", "issuingCountry", "nationality"}, "country-name": {"country", "issuingCountry", "nationality"},
	"bday": {"dateOfBirth"}, "sex": {"sex"}, "username": {"username"},
	"current-password": {"password"}, "new-password": {"password"},
}

// hintTokens name a control by the words around it when it has no autocomplete token, most
// specific first.
var hintTokens = []struct {
	re    *regexp.Regexp
	token string
}{
	{regexp.MustCompile(`card.?(number|num|no)\b|cc.?(num|number)|credit.?card`), "cc-number"},
	{regexp.MustCompile(`cvv|cvc|csc|security.?code`), "cc-csc"},
	{regexp.MustCompile(`(name.?on.?card|card.?holder|cc.?name)`), "cc-name"},
	{regexp.MustCompile(`exp.*month|expiry.?m|exp.?mm`), "cc-exp-month"},
	{regexp.MustCompile(`exp.*year|expiry.?y|exp.?yy`), "cc-exp-year"},
	{regexp.MustCompile(`expir|exp.?date|valid.?thru`), "cc-exp"},
	{regexp.MustCompile(`first.?name|given.?name|fname`), "given-name"},
	{regexp.MustCompile(`last.?name|sur.?name|family.?name|lname`), "family-name"},
	{regexp.MustCompile(`middle`), "additional-name"},
	{regexp.MustCompile(`full.?name|^name\b`), "name"},
	{regexp.MustCompile(`address.?(line)?.?(1|one)\b|street`), "address-line1"},
	{regexp.MustCompile(`address.?(line)?.?(2|two)\b|apt|apartment|suite|unit\b`), "address-line2"},
	{regexp.MustCompile(`\bcity\b|town`), "address-level2"},
	{regexp.MustCompile(`\bstate\b|province|region`), "address-level1"},
	{regexp.MustCompile(`zip|postal|post.?code`), "postal-code"},
	{regexp.MustCompile(`country`), "country"},
	{regexp.MustCompile(`phone|mobile|\btel\b`), "tel"},
	{regexp.MustCompile(`e-?mail`), "email"},
	{regexp.MustCompile(`passport`), "passport-number"},
	{regexp.MustCompile(`licen[cs]e`), "license-number"},
	{regexp.MustCompile(`birth|dob\b`), "bday"},
}

// tokenOf is the autocomplete token that names the control: its own, else one read from its
// words. "name" and "cc-exp" stand for several Chaavi fields.
func tokenOf(f formField) string {
	for _, word := range strings.Fields(f.Auto) {
		if _, ok := tokenFields[word]; ok || word == "name" || word == "cc-exp" || word == "street-address" {
			return word
		}
	}
	for _, h := range hintTokens {
		if h.re.MatchString(f.Hint) {
			return h.token
		}
	}
	return ""
}

// fieldValue resolves token to a value of item, or "" when the item has nothing for it. Value
// is read through the client, so secret says whether Chaavi keeps it secret.
func fieldValue(c *vault.Client, item vault.Item, token string) (field, value string, secret bool, err error) {
	has := map[string]bool{}
	for _, n := range item.Names() {
		has[n] = true
	}
	read := func(names ...string) (string, string, bool, error) {
		var parts []string
		var secret bool
		for _, n := range names {
			if !has[n] {
				continue
			}
			v, s, err := c.Value(item, n)
			if err != nil {
				return "", "", false, err
			}
			parts = append(parts, v)
			secret = secret || s
		}
		return strings.Join(names, "+"), strings.Join(parts, " "), secret, nil
	}
	switch token {
	case "name":
		if has["firstName"] || has["lastName"] {
			return read("firstName", "middleName", "lastName")
		}
		return read("givenName", "surname")
	case "street-address":
		return read("address1", "address2", "address3")
	case "cc-exp":
		if !has["expMonth"] || !has["expYear"] {
			return "", "", false, nil
		}
		m, _, err := c.Value(item, "expMonth")
		if err != nil {
			return "", "", false, err
		}
		y, _, err := c.Value(item, "expYear")
		if err != nil {
			return "", "", false, err
		}
		if len(y) == 4 {
			y = y[2:]
		}
		return "expMonth+expYear", fmt.Sprintf("%02s/%s", m, y), false, nil
	case "passport-number":
		return read("passportNumber")
	case "license-number":
		return read("licenseNumber")
	}
	for _, name := range tokenFields[token] {
		if has[name] {
			v, s, err := c.Value(item, name)
			return name, v, s, err
		}
	}
	return "", "", false, nil
}

// formFields lists the fillable controls in every frame.
func (t *Tab) formFields(ctx context.Context, scope string) ([]formField, error) {
	frames, err := t.framesInOrder(ctx)
	if err != nil {
		return nil, err
	}
	var all []formField
	for _, f := range frames {
		t.mu.Lock()
		start := t.nextRef + 1
		t.mu.Unlock()
		var got struct {
			Fields []formField `json:"fields"`
			Next   int         `json:"next"`
		}
		if err := t.eval(ctx, f, fmt.Sprintf("(%s)(%d, %s)", vaultFieldsScript, start, jsonString(scope)), &got); err != nil {
			return nil, err
		}
		t.mu.Lock()
		if got.Next-1 > t.nextRef {
			t.nextRef = got.Next - 1
		}
		t.mu.Unlock()
		all = append(all, got.Fields...)
	}
	return all, nil
}

// typeSecret types a vault value into the control, marking it so outlines show it as filled.
func (t *Tab) typeSecret(ctx context.Context, ref, text string, submit bool) (string, error) {
	el, err := t.resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	if err := el.call(ctx, markSecretFn, nil); err != nil {
		return "", fmt.Errorf("ref %s: %w", ref, err)
	}
	return t.Type(ctx, ref, text, submit)
}

// putValue puts a vault value into the control: a select takes the option it names (a month
// or year in any common spelling), anything else is typed.
func (t *Tab) putValue(ctx context.Context, ref string, isSelect bool, value string, secret, submit bool) (string, error) {
	if !isSelect {
		if secret {
			return t.typeSecret(ctx, ref, value, submit)
		}
		return t.Type(ctx, ref, value, submit)
	}
	var lastErr error
	for _, want := range selectSpellings(value) {
		var err error
		if _, err = t.Select(ctx, ref, want); err == nil {
			return "", nil
		}
		lastErr = err
	}
	return "", lastErr
}

// selectSpellings are the forms an option may name value in: as given, without leading
// zeros, a four-digit year as two digits, and a two-digit year as four.
func selectSpellings(value string) []string {
	forms := []string{value}
	if trimmed := strings.TrimLeft(value, "0"); trimmed != "" && trimmed != value {
		forms = append(forms, trimmed)
	}
	if len(value) == 4 {
		forms = append(forms, value[2:])
	}
	if len(value) == 2 {
		forms = append(forms, "20"+value)
	}
	return forms
}

// vaultFind lists the Chaavi items matching the query: names, ids and non-secret values.
func (k *Kit) vaultFind(in input) (string, error) {
	items, err := k.chaavi.Items(vault.Query{Q: in.Query, URI: in.Site, Type: in.Kind})
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "no Chaavi items match", nil
	}
	var b strings.Builder
	for i, it := range items {
		if i == maxVaultItems {
			fmt.Fprintf(&b, "(%d more: narrow query, site or kind)\n", len(items)-i)
			break
		}
		b.WriteString("- " + it.Describe() + "\n")
	}
	return b.String(), nil
}

// vaultType types one field of a Chaavi item into the control at ref.
func (k *Kit) vaultType(ctx context.Context, s *session, tab *Tab, in input) (string, error) {
	if in.Ref == "" || in.Field == "" {
		return "", errors.New("ref and field are required")
	}
	item, err := k.chaavi.Resolve(in.Item, "")
	if err != nil {
		return "", err
	}
	value, secret, err := k.chaavi.Value(item, in.Field)
	if err != nil {
		return "", err
	}
	if secret {
		s.secrets = append(s.secrets, value)
	}
	note, err := tab.putValue(ctx, in.Ref, tabIsSelect(ctx, tab, in.Ref), value, secret, in.Submit)
	if err != nil {
		return "", err
	}
	out := fmt.Sprintf("typed %s of %s into %s", in.Field, jsonString(item.Name), in.Ref)
	if note != "" {
		out += "; " + note
	}
	return out, nil
}

// tabIsSelect reports whether the control at ref is a select.
func tabIsSelect(ctx context.Context, tab *Tab, ref string) bool {
	el, err := tab.resolve(ctx, ref)
	if err != nil {
		return false
	}
	var is bool
	return el.call(ctx, `function() { return this instanceof HTMLSelectElement; }`, &is) == nil && is
}

// vaultFill fills every control of the page's form (or of scope) that the item has a value
// for, and names the controls it left alone.
func (k *Kit) vaultFill(ctx context.Context, s *session, tab *Tab, in input) (string, error) {
	item, err := k.chaavi.Resolve(in.Item, "")
	if err != nil {
		return "", err
	}
	fields, err := tab.formFields(ctx, in.Scope)
	if err != nil {
		return "", err
	}
	if len(fields) == 0 {
		return "", errors.New("the page shows no form fields to fill; navigate to the form, or pass scope as the ref of the form")
	}
	var filled, left []string
	for _, f := range fields {
		token := tokenOf(f)
		if token == "" {
			left = append(left, f.Ref+" "+jsonString(clip(f.Hint, 40)))
			continue
		}
		name, value, secret, err := fieldValue(k.chaavi, item, token)
		if err != nil {
			return "", err
		}
		if value == "" {
			left = append(left, f.Ref+" "+jsonString(clip(f.Hint, 40))+" (the item has no "+token+")")
			continue
		}
		if secret {
			s.secrets = append(s.secrets, value)
		}
		if _, err := tab.putValue(ctx, f.Ref, f.Select, value, secret, false); err != nil {
			return "", fmt.Errorf("%s: %s", f.Ref, strings.ReplaceAll(err.Error(), value, "***"))
		}
		filled = append(filled, f.Ref+"="+name)
	}
	out := fmt.Sprintf("filled %d fields from %s: %s", len(filled), jsonString(item.Name), strings.Join(filled, ", "))
	if len(left) > 0 {
		out += "\nleft alone: " + strings.Join(left, "; ")
	}
	if len(filled) == 0 {
		return "", errors.New(out)
	}
	return out, nil
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// vaultSaveLogin saves a new login for a site with a password Chaavi makes.
func (k *Kit) vaultSaveLogin(in input) (string, error) {
	if in.Item == "" || in.Site == "" {
		return "", errors.New("item (the login's name) and site are required")
	}
	existing, err := k.chaavi.Items(vault.Query{Type: "login"})
	if err != nil {
		return "", err
	}
	for _, it := range existing {
		if strings.EqualFold(it.Name, in.Item) {
			return "", fmt.Errorf("Chaavi already has a login named %s (id %s); use it, or pick another name", jsonString(it.Name), it.ID)
		}
	}
	item, err := k.chaavi.CreateLogin(in.Item, in.Username, in.Site, in.Length, true)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("saved the Chaavi login %s (id %s) with a generated password; type it with vault_type (item, field password)", jsonString(item.Name), item.ID), nil
}

// vaultSetTotp stores the authenticator key a site showed on a login.
func (k *Kit) vaultSetTotp(s *session, in input) (string, error) {
	if in.Key == "" {
		return "", errors.New("key is required: the authenticator key or otpauth:// link the site showed")
	}
	item, err := k.chaavi.Resolve(in.Item, "login")
	if err != nil {
		return "", err
	}
	if err := k.chaavi.SetTotp(item, in.Key); err != nil {
		return "", err
	}
	s.secrets = append(s.secrets, in.Key)
	return fmt.Sprintf("stored the authenticator key on %s; sign_in with use totp, or vault_type field totp, makes its codes", jsonString(item.Name)), nil
}
