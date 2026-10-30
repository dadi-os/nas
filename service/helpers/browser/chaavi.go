package browser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// chaaviTimeout bounds one Chaavi request.
const chaaviTimeout = 30 * time.Second

// chaaviClient reads logins and passkeys from Chaavi's /v1 API. What it returns goes straight
// into a page and is never shown to the model.
type chaaviClient struct {
	base string
	http *http.Client
}

type chaaviItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Login *struct {
		Username *string `json:"username"`
		Passkeys []struct {
			RPID string `json:"rpId"`
		} `json:"passkeys"`
	} `json:"login"`
}

// call sends a JSON request to path and decodes a 2xx answer into out.
func (c *chaaviClient) call(method, path string, query url.Values, out any) error {
	target := c.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, target, bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
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
	return json.Unmarshal(raw, out)
}

// logins lists the login items whose address matches site.
func (c *chaaviClient) logins(site string) ([]chaaviItem, error) {
	var out struct {
		Items []chaaviItem `json:"items"`
	}
	if err := c.call(http.MethodGet, "/v1/items", url.Values{"uri": {site}, "type": {"login"}}, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// credentials decrypts a login's username and password.
func (c *chaaviClient) credentials(id string) (username, password string, err error) {
	var out struct {
		Username *string `json:"username"`
		Password string  `json:"password"`
	}
	if err := c.call(http.MethodPost, "/v1/items/"+url.PathEscape(id)+"/login", nil, &out); err != nil {
		return "", "", err
	}
	if out.Username != nil {
		username = *out.Username
	}
	return username, out.Password, nil
}

// passkey decrypts a login's passkey.
func (c *chaaviClient) passkey(id string) (Passkey, error) {
	var out Passkey
	err := c.call(http.MethodPost, "/v1/items/"+url.PathEscape(id)+"/passkey", nil, &out)
	return out, err
}
