// Package auth verifies Keycloak-issued JWT access tokens.
package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/coreos/go-oidc/v3/oidc"
)

// ErrInvalidToken is returned when a token fails signature, issuer, audience or expiry checks.
var ErrInvalidToken = errors.New("invalid access token")

// Principal is the authenticated caller extracted from a verified token.
type Principal struct {
	Subject  string
	Username string
	Roles    []string
}

// HasRole reports whether the principal has the given realm role.
func (p Principal) HasRole(role string) bool {
	return slices.Contains(p.Roles, role)
}

// Verifier validates access tokens: RS256 signature by the IdP key set, issuer, audience and expiry.
type Verifier struct {
	verifier *oidc.IDTokenVerifier
}

// NewVerifier creates a verifier. audience may be empty to skip the audience check.
func NewVerifier(issuer string, keySet oidc.KeySet, audience string) *Verifier {
	cfg := &oidc.Config{
		ClientID:             audience,
		SkipClientIDCheck:    audience == "",
		SupportedSigningAlgs: []string{oidc.RS256},
	}
	return &Verifier{verifier: oidc.NewVerifier(issuer, keySet, cfg)}
}

// NewRemoteVerifier creates a verifier that fetches signing keys from the IdP JWKS endpoint.
func NewRemoteVerifier(ctx context.Context, issuer, jwksURL, audience string) *Verifier {
	return NewVerifier(issuer, oidc.NewRemoteKeySet(ctx, jwksURL), audience)
}

type accessClaims struct {
	Subject           string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

// Verify checks the raw token and returns the principal it represents.
func (v *Verifier) Verify(ctx context.Context, raw string) (Principal, error) {
	tok, err := v.verifier.Verify(ctx, raw)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	var c accessClaims
	if err := tok.Claims(&c); err != nil {
		return Principal{}, fmt.Errorf("%w: decode claims: %v", ErrInvalidToken, err)
	}
	if c.Subject == "" {
		return Principal{}, fmt.Errorf("%w: empty subject", ErrInvalidToken)
	}
	return Principal{Subject: c.Subject, Username: c.PreferredUsername, Roles: c.RealmAccess.Roles}, nil
}
