package vault

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const itemsJSON = `{"items":[
 {"id":"l1","name":"Amazon","type":"login","hasNotes":false,"fields":[{"name":"pin","type":"hidden","hasValue":true},{"name":"region","type":"text","value":"us"}],
  "login":{"username":"ankur@example.com","uris":[{"uri":"https://amazon.com"}],"passkeys":[],"hasPassword":true,"hasTotp":true}},
 {"id":"c1","name":"Visa","type":"card","hasNotes":false,"fields":[],"card":{"cardholderName":"Ankur Desai","brand":"Visa","expMonth":"07","expYear":"2029","hasNumber":true,"hasCode":true}},
 {"id":"i1","name":"Home","type":"identity","hasNotes":true,"fields":[],"identity":{"firstName":"Ankur","lastName":"Desai","address1":"1 Main St","city":"Lansing","postalCode":"48823","hasSsn":true,"hasPassportNumber":false}},
 {"id":"n1","name":"Home","type":"note","hasNotes":false,"fields":[]}
]}`

// fake serves itemsJSON and records reveal and create bodies.
func fake(t *testing.T, bodies *[]map[string]any) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/items", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(itemsJSON)) })
	mux.HandleFunc("POST /v1/items", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*bodies = append(*bodies, body)
		_, _ = w.Write([]byte(`{"id":"new","name":"Shop","type":"login","login":{"hasPassword":true}}`))
	})
	mux.HandleFunc("POST /v1/items/{id}/reveal", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*bodies = append(*bodies, body)
		values := map[string]string{"password": "pw-1", "number": "4242424242424242", "code": "123", "ssn": "078-05-1120", "pin": "9999"}
		field, _ := body["field"].(string)
		if field == "custom" {
			field = body["name"].(string)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"value": values[field]})
	})
	mux.HandleFunc("POST /v1/items/{id}/totp", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":"424242","period":30,"remaining":9}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func TestResolveByIDAndNameAndAmbiguity(t *testing.T) {
	c := fake(t, new([]map[string]any))
	if it, err := c.Resolve("visa", "card"); err != nil || it.ID != "c1" {
		t.Fatalf("by name: %v %v", it.ID, err)
	}
	if it, err := c.Resolve("i1", ""); err != nil || it.Name != "Home" {
		t.Fatalf("by id: %v %v", it.Name, err)
	}
	if _, err := c.Resolve("Home", ""); err == nil || !strings.Contains(err.Error(), "2 items named") {
		t.Fatalf("ambiguous: %v", err)
	}
	if _, err := c.Resolve("Nope", ""); err == nil {
		t.Fatal("resolved a missing item")
	}
}

func TestDescribeShowsPublicValuesAndNamesSecretsOnly(t *testing.T) {
	c := fake(t, new([]map[string]any))
	items, _ := c.Items(Query{})
	card := items[1].Describe()
	for _, want := range []string{"brand=Visa", "expMonth=07", "secret fields (never shown): code, number"} {
		if !strings.Contains(card, want) {
			t.Errorf("card %q lacks %q", card, want)
		}
	}
	id := items[2].Describe()
	if !strings.Contains(id, "address1=1 Main St") || !strings.Contains(id, "ssn") || strings.Contains(id, "passportNumber") || !strings.Contains(id, "notes") {
		t.Errorf("identity: %q", id)
	}
	if login := items[0].Describe(); !strings.Contains(login, "password, totp") || !strings.Contains(login, "custom:pin (hidden)") || !strings.Contains(login, "amazon.com") {
		t.Errorf("login: %q", login)
	}
}

func TestValueReadsPlainSecretTotpAndCustomFields(t *testing.T) {
	var bodies []map[string]any
	c := fake(t, &bodies)
	items, _ := c.Items(Query{})
	login, card, id := items[0], items[1], items[2]
	cases := []struct {
		item   Item
		field  string
		want   string
		secret bool
	}{
		{login, "username", "ankur@example.com", false},
		{login, "password", "pw-1", true},
		{login, "totp", "424242", true},
		{login, "custom:pin", "9999", true},
		{login, "custom:region", "us", false},
		{card, "expMonth", "07", false},
		{card, "number", "4242424242424242", true},
		{id, "city", "Lansing", false},
		{id, "ssn", "078-05-1120", true},
	}
	for _, tc := range cases {
		got, secret, err := c.Value(tc.item, tc.field)
		if err != nil || got != tc.want || secret != tc.secret {
			t.Errorf("%s %s = %q secret %v err %v, want %q secret %v", tc.item.Name, tc.field, got, secret, err, tc.want, tc.secret)
		}
	}
	if _, _, err := c.Value(id, "passportNumber"); err == nil || !strings.Contains(err.Error(), "it has:") {
		t.Errorf("a missing secret: %v", err)
	}
	if _, _, err := c.Value(card, "bogus"); err == nil {
		t.Error("an unknown field resolved")
	}
}

func TestCreateLoginAsksChaaviToMakeThePassword(t *testing.T) {
	var bodies []map[string]any
	c := fake(t, &bodies)
	if _, err := c.CreateLogin("Shop", "me@example.com", "https://shop.test", 24, true); err != nil {
		t.Fatal(err)
	}
	login := bodies[0]["login"].(map[string]any)
	if bodies[0]["type"] != "login" || login["length"] != float64(24) || login["special"] != true || login["password"] != nil {
		t.Fatalf("body %v", bodies[0])
	}
}

func TestScrubHidesASecretInItsCommonSpellings(t *testing.T) {
	got := Scrub("card 4242 4242 4242 4242 / 4242424242424242 / 4242-4242-4242-4242 / pw-1", []string{"4242424242424242", "pw-1"})
	if strings.Contains(got, "4242") || strings.Contains(got, "pw-1") {
		t.Fatalf("not scrubbed: %q", got)
	}
	if Scrub("ok 12", []string{"12"}) != "ok 12" {
		t.Fatal("scrubbed a value too short to be a secret")
	}
}
