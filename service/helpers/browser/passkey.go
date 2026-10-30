package browser

import (
	"context"
	"fmt"
	"strings"
)

// Passkey is a PKCS#8 passkey as Chaavi hands it out, loaded into a virtual authenticator.
// Its fields are never logged or returned to the model.
type Passkey struct {
	CredentialID string `json:"credentialId"`
	RPID         string `json:"rpId"`
	PrivateKey   string `json:"privateKey"`
	UserHandle   string `json:"userHandle"`
	SignCount    int    `json:"signCount"`
	Resident     bool   `json:"resident"`
}

// AddPasskey loads the passkey into this tab's virtual authenticator, replacing any passkey
// loaded before, then reloads the tab so a site that already asked for a WebAuthn answer asks
// again. The authenticator lives on the tab's own kept session, so it stays active across
// later actions on the tab: Chromium turns it off when any other session on the tab detaches,
// which is why no command here opens a session of its own.
func (t *Tab) AddPasskey(ctx context.Context, p Passkey) error {
	t.mu.Lock()
	auth := t.authID
	t.mu.Unlock()
	if auth == "" {
		if err := t.b.c.send(ctx, t.sid, "WebAuthn.enable", nil, nil); err != nil {
			return err
		}
		var added struct {
			AuthenticatorID string `json:"authenticatorId"`
		}
		err := t.b.c.send(ctx, t.sid, "WebAuthn.addVirtualAuthenticator", map[string]any{"options": map[string]any{
			"protocol": "ctap2", "transport": "internal", "hasResidentKey": true, "hasUserVerification": true,
			"isUserVerified": true, "automaticPresenceSimulation": true,
		}}, &added)
		if err != nil {
			return err
		}
		auth = added.AuthenticatorID
		t.mu.Lock()
		t.authID = auth
		t.mu.Unlock()
	}
	if err := t.b.c.send(ctx, t.sid, "WebAuthn.clearCredentials", map[string]any{"authenticatorId": auth}, nil); err != nil {
		return err
	}
	err := t.b.c.send(ctx, t.sid, "WebAuthn.addCredential", map[string]any{"authenticatorId": auth, "credential": map[string]any{
		"credentialId": p.CredentialID, "isResidentCredential": p.Resident, "rpId": p.RPID,
		"privateKey": p.PrivateKey, "userHandle": p.UserHandle, "signCount": p.SignCount,
	}}, nil)
	if err != nil {
		return fmt.Errorf("load passkey: %w", scrub(err, p.PrivateKey, p.CredentialID))
	}
	if err := t.b.c.send(ctx, t.sid, "Page.reload", nil, nil); err != nil {
		return err
	}
	return t.waitLoad(ctx, "load")
}

// scrub removes the secrets from an error message.
func scrub(err error, secrets ...string) error {
	msg := err.Error()
	for _, s := range secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "***")
		}
	}
	return fmt.Errorf("%s", msg)
}
