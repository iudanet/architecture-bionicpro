// Package authtest issues signed JWTs for tests, imitating the Keycloak realm.
package authtest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/require"
)

// Issuer is the test issuer URL.
const Issuer = "http://idp.test/realms/reports-realm"

// TokenIssuer signs tokens with a freshly generated RSA key.
type TokenIssuer struct {
	t   *testing.T
	key *rsa.PrivateKey
}

// New creates a token issuer with a new RSA key.
func New(t *testing.T) *TokenIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return &TokenIssuer{t: t, key: key}
}

// KeySet returns the key set that verifies tokens of this issuer.
func (i *TokenIssuer) KeySet() oidc.KeySet {
	return &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{i.key.Public()}}
}

// Sign signs arbitrary claims.
func (i *TokenIssuer) Sign(claims map[string]any) string {
	i.t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: i.key}, nil)
	require.NoError(i.t, err)
	payload, err := json.Marshal(claims)
	require.NoError(i.t, err)
	jws, err := signer.Sign(payload)
	require.NoError(i.t, err)
	raw, err := jws.CompactSerialize()
	require.NoError(i.t, err)
	return raw
}

// AccessToken returns a valid access token for subject with the given realm roles.
func (i *TokenIssuer) AccessToken(subject, audience string, roles ...string) string {
	return i.Sign(Claims(subject, audience, roles...))
}

// Claims returns default access token claims that callers may tweak before signing.
func Claims(subject, audience string, roles ...string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss":                Issuer,
		"sub":                subject,
		"aud":                []string{audience, "account"},
		"exp":                now.Add(5 * time.Minute).Unix(),
		"iat":                now.Unix(),
		"preferred_username": "prothetic1",
		"realm_access":       map[string]any{"roles": roles},
	}
}
