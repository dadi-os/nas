// Package vault is the browser and terminal helpers' one client for Chaavi's /v1 credential
// API. It reads every item type, resolves any field of an item to its value, and makes
// one-time codes. What Value returns goes straight into a page or a command and is never
// shown to the model; Describe shows only what Chaavi lists as non-secret.
package vault

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Timeout bounds one Chaavi request.
const Timeout = 30 * time.Second

// Client calls Chaavi at Base.
type Client struct {
	Base string
	HTTP *http.Client
}

// New is a Client for the Chaavi at base.
func New(base string) *Client {
	return &Client{Base: base, HTTP: &http.Client{Timeout: Timeout}}
}

// Field is a custom field of an item. A hidden field's value is never listed.
type Field struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Value    any    `json:"value"`
	HasValue bool   `json:"hasValue"`
}

// Login is the login part of an item.
type Login struct {
	Username *string `json:"username"`
	URIs     []struct {
		URI string `json:"uri"`
	} `json:"uris"`
	Passkeys []struct {
		RPID string `json:"rpId"`
	} `json:"passkeys"`
	HasPassword bool `json:"hasPassword"`
	HasTotp     bool `json:"hasTotp"`
}

// Item is a listed Chaavi item: every non-secret field, and only flags for the secret ones.
type Item struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	FolderID *string `json:"folderId"`
	HasNotes bool    `json:"hasNotes"`
	Fields   []Field `json:"fields"`
	Login    *Login  `json:"login"`
	// Detail is the item's own object for a card, identity, bank account, driver's license,
	// passport or SSH key: plain values by name, and hasX flags for its secrets.
	Detail map[string]any `json:"-"`
}

// UnmarshalJSON decodes an item and keeps the object named by its type as Detail.
func (i *Item) UnmarshalJSON(raw []byte) error {
	type plain Item
	if err := json.Unmarshal(raw, (*plain)(i)); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return err
	}
	if own, ok := all[i.Type]; ok && i.Type != "login" {
		return json.Unmarshal(own, &i.Detail)
	}
	return nil
}

// Query narrows a listing; an empty one lists every item.
type Query struct {
	Q, URI, Type, Folder string
}

// Call sends a JSON request to path and decodes a 2xx answer into out (when out is not nil).
func (c *Client) Call(method, path string, query url.Values, body, out any) error {
	target := c.Base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	payload := []byte("{}")
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequest(method, target, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("chaavi unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var failed struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &failed) == nil && failed.Error.Type != "" {
			return fmt.Errorf("chaavi %s: %s", failed.Error.Type, failed.Error.Message)
		}
		return fmt.Errorf("chaavi %s %s: %d", method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Items lists the items matching q.
func (c *Client) Items(q Query) ([]Item, error) {
	v := url.Values{}
	for key, value := range map[string]string{"q": q.Q, "uri": q.URI, "type": q.Type, "folder": q.Folder} {
		if value != "" {
			v.Set(key, value)
		}
	}
	var out struct {
		Items []Item `json:"items"`
	}
	if err := c.Call(http.MethodGet, "/v1/items", v, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// Resolve is the one item that ref names: its id, or its name without regard to case. typ
// narrows by item type when not empty.
func (c *Client) Resolve(ref, typ string) (Item, error) {
	if strings.TrimSpace(ref) == "" {
		return Item{}, fmt.Errorf("name the Chaavi item (its name or id)")
	}
	items, err := c.Items(Query{Type: typ})
	if err != nil {
		return Item{}, err
	}
	return pick(items, ref)
}

func pick(items []Item, ref string) (Item, error) {
	var named []Item
	for _, it := range items {
		if it.ID == ref {
			return it, nil
		}
		if strings.EqualFold(it.Name, ref) {
			named = append(named, it)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		return Item{}, fmt.Errorf("Chaavi has no item named %q", ref)
	}
	var ids []string
	for _, it := range named {
		ids = append(ids, it.ID)
	}
	return Item{}, fmt.Errorf("Chaavi has %d items named %q (ids %s); pass the id", len(named), ref, strings.Join(ids, ", "))
}

// Names are the field names Value accepts for the item: plain ones and secret ones, and
// custom fields as custom:<name>.
func (i Item) Names() []string {
	var names []string
	switch i.Type {
	case "login":
		names = append(names, "username")
		if i.Login != nil && i.Login.HasPassword {
			names = append(names, "password")
		}
		if i.Login != nil && i.Login.HasTotp {
			names = append(names, "totp")
		}
	default:
		for key, v := range i.Detail {
			if flag, ok := strings.CutPrefix(key, "has"); ok && flag != "" {
				if on, _ := v.(bool); on {
					names = append(names, strings.ToLower(flag[:1])+flag[1:])
				}
				continue
			}
			if s, _ := v.(string); s != "" {
				names = append(names, key)
			}
		}
	}
	if i.HasNotes {
		names = append(names, "notes")
	}
	for _, f := range i.Fields {
		names = append(names, "custom:"+f.Name)
	}
	sort.Strings(names)
	return names
}

// Describe is what the model may see of the item: its name, id, type, username, addresses,
// every non-secret value Chaavi lists, and the names of its secrets and custom fields. A
// secret's value is never in it.
func (i Item) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s, id %s)", i.Name, i.Type, i.ID)
	if l := i.Login; l != nil {
		if l.Username != nil && *l.Username != "" {
			fmt.Fprintf(&b, "; username %s", *l.Username)
		}
		for _, u := range l.URIs {
			fmt.Fprintf(&b, "; site %s", u.URI)
		}
		for _, p := range l.Passkeys {
			fmt.Fprintf(&b, "; passkey for %s", p.RPID)
		}
	}
	var plain, secret []string
	for key, v := range i.Detail {
		if flag, ok := strings.CutPrefix(key, "has"); ok && flag != "" {
			if on, _ := v.(bool); on {
				secret = append(secret, strings.ToLower(flag[:1])+flag[1:])
			}
			continue
		}
		if s, _ := v.(string); s != "" {
			plain = append(plain, key+"="+s)
		}
	}
	sort.Strings(plain)
	sort.Strings(secret)
	if len(plain) > 0 {
		fmt.Fprintf(&b, "; %s", strings.Join(plain, ", "))
	}
	if i.Login != nil {
		if i.Login.HasPassword {
			secret = append(secret, "password")
		}
		if i.Login.HasTotp {
			secret = append(secret, "totp")
		}
	}
	if i.HasNotes {
		secret = append(secret, "notes")
	}
	if len(secret) > 0 {
		fmt.Fprintf(&b, "; secret fields (never shown): %s", strings.Join(secret, ", "))
	}
	for _, f := range i.Fields {
		fmt.Fprintf(&b, "; custom:%s (%s)", f.Name, f.Type)
	}
	return b.String()
}

// Value resolves one field of the item to its value. secret says whether Chaavi keeps the
// field secret, so the caller scrubs it from everything the model reads. Fields: username,
// password and totp of a login (totp is the current code); a card's, identity's, bank
// account's, license's or passport's own fields by Chaavi's names, such as number, code,
// expMonth, address1, ssn, passportNumber; notes; and custom:<name> for a custom field.
func (c *Client) Value(i Item, field string) (value string, secret bool, err error) {
	id := url.PathEscape(i.ID)
	reveal := func(body map[string]string) (string, error) {
		var out struct {
			Value string `json:"value"`
		}
		if err := c.Call(http.MethodPost, "/v1/items/"+id+"/reveal", nil, body, &out); err != nil {
			return "", err
		}
		return out.Value, nil
	}
	switch {
	case field == "notes":
		value, err = reveal(map[string]string{"field": "notes"})
		return value, true, err
	case strings.HasPrefix(field, "custom:"):
		name := strings.TrimPrefix(field, "custom:")
		for _, f := range i.Fields {
			if f.Name != name {
				continue
			}
			if f.Type == "hidden" {
				value, err = reveal(map[string]string{"field": "custom", "name": name})
				return value, true, err
			}
			return fmt.Sprint(f.Value), false, nil
		}
	case i.Type == "login" && field == "username":
		if i.Login == nil || i.Login.Username == nil || *i.Login.Username == "" {
			return "", false, fmt.Errorf("the Chaavi login %q has no username", i.Name)
		}
		return *i.Login.Username, false, nil
	case i.Type == "login" && field == "totp":
		if i.Login == nil || !i.Login.HasTotp {
			return "", false, fmt.Errorf("the Chaavi login %q has no authenticator key", i.Name)
		}
		var out struct {
			Code string `json:"code"`
		}
		err = c.Call(http.MethodPost, "/v1/items/"+id+"/totp", nil, nil, &out)
		return out.Code, true, err
	case i.Type == "login" && field == "password":
		if i.Login == nil || !i.Login.HasPassword {
			return "", false, fmt.Errorf("the Chaavi login %q has no password", i.Name)
		}
		value, err = reveal(map[string]string{"field": "password"})
		return value, true, err
	case i.Detail != nil:
		if s, ok := i.Detail[field].(string); ok && s != "" {
			return s, false, nil
		}
		flag := "has" + strings.ToUpper(field[:1]) + field[1:]
		if on, _ := i.Detail[flag].(bool); on {
			value, err = reveal(map[string]string{"field": field})
			return value, true, err
		}
	}
	return "", false, fmt.Errorf("the Chaavi item %q has no %q; it has: %s", i.Name, field, strings.Join(i.Names(), ", "))
}

// CreateLogin saves a new login and has Chaavi make its password, so the password is never
// seen. uri is the site it is for; length 0 takes Chaavi's default.
func (c *Client) CreateLogin(name, username, uri string, length int, special bool) (Item, error) {
	login := map[string]any{"special": special}
	if username != "" {
		login["username"] = username
	}
	if uri != "" {
		login["uris"] = []map[string]string{{"uri": uri}}
	}
	if length > 0 {
		login["length"] = length
	}
	var out Item
	err := c.Call(http.MethodPost, "/v1/items", nil, map[string]any{"type": "login", "name": name, "login": login}, &out)
	return out, err
}

// SetTotp stores an authenticator key (a base32 key or an otpauth:// URI) on a login.
func (c *Client) SetTotp(i Item, key string) error {
	if i.Type != "login" {
		return fmt.Errorf("the Chaavi item %q is not a login", i.Name)
	}
	return c.Call(http.MethodPatch, "/v1/items/"+url.PathEscape(i.ID), nil, map[string]any{"login": map[string]string{"totp": key}}, nil)
}

// Scrub replaces each secret, and its common spellings, with *** in text.
func Scrub(text string, secrets []string) string {
	for _, s := range secrets {
		for _, form := range spellings(s) {
			text = strings.ReplaceAll(text, form, "***")
		}
	}
	return text
}

// spellings are the forms a page may show a secret in: as given, and for a number the digits
// alone and in groups of four.
func spellings(s string) []string {
	if len(s) < 4 {
		return nil
	}
	forms := []string{s}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	if len(digits) >= 8 && len(digits) == len(strings.NewReplacer(" ", "", "-", "").Replace(s)) {
		forms = append(forms, digits)
		var grouped []string
		for i := 0; i < len(digits); i += 4 {
			grouped = append(grouped, digits[i:min(i+4, len(digits))])
		}
		forms = append(forms, strings.Join(grouped, " "), strings.Join(grouped, "-"))
	}
	return forms
}
