// Package bff implements the Backend-for-Frontend: it is the only OAuth 2.0 client of the IdP,
// keeps tokens server-side and gives the browser only an HttpOnly session cookie.
package bff

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/iudanet/architecture-bionicpro/backend/internal/auth"
)

// UserInfo is the user profile exposed to the frontend (no tokens).
type UserInfo struct {
	Subject  string   `json:"sub"`
	Username string   `json:"username"`
	Name     string   `json:"name"`
	Roles    []string `json:"roles"`
}

// Tokens is the token set obtained from the IdP. It never leaves the BFF.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	Nonce        string
	User         UserInfo
}

// Authenticator is the OIDC client used by the BFF.
type Authenticator interface {
	// AuthCodeURL builds the authorization URL with state, nonce and the S256 PKCE challenge of verifier.
	AuthCodeURL(state, nonce, verifier string) string
	// Exchange swaps the authorization code and PKCE verifier for verified tokens.
	Exchange(ctx context.Context, code, verifier string) (*Tokens, error)
	// Refresh obtains a new token set with the refresh token.
	Refresh(ctx context.Context, refreshToken string) (*Tokens, error)
	// Logout terminates the IdP session of the refresh token.
	Logout(ctx context.Context, refreshToken string) error
}

// KeycloakConfig configures the Keycloak OIDC client.
type KeycloakConfig struct {
	// PublicIssuer is the realm URL as seen by the browser; it is the "iss" of tokens.
	PublicIssuer string
	// InternalIssuer is the realm URL reachable from the BFF (back channel).
	InternalIssuer string
	ClientID       string
	ClientSecret   string
	RedirectURL    string
	// APIAudience is the audience the access token must contain (reports-api).
	APIAudience string
}

// Keycloak implements Authenticator for a Keycloak realm.
type Keycloak struct {
	oauth      *oauth2.Config
	idVerifier *oidc.IDTokenVerifier
	atVerifier *auth.Verifier
	logoutURL  string
	httpClient *http.Client
}

// NewKeycloak creates the client. Endpoints are built explicitly: the browser uses the public URL,
// token/JWKS/logout requests go over the internal network.
func NewKeycloak(ctx context.Context, cfg KeycloakConfig, httpClient *http.Client) *Keycloak {
	pub := strings.TrimRight(cfg.PublicIssuer, "/")
	internal := strings.TrimRight(cfg.InternalIssuer, "/")
	keyCtx := oidc.ClientContext(ctx, httpClient)
	keys := oidc.NewRemoteKeySet(keyCtx, internal+"/protocol/openid-connect/certs")
	return &Keycloak{
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
			Endpoint: oauth2.Endpoint{
				AuthURL:   pub + "/protocol/openid-connect/auth",
				TokenURL:  internal + "/protocol/openid-connect/token",
				AuthStyle: oauth2.AuthStyleInHeader,
			},
		},
		idVerifier: oidc.NewVerifier(pub, keys, &oidc.Config{ClientID: cfg.ClientID}),
		atVerifier: auth.NewVerifier(pub, keys, cfg.APIAudience),
		logoutURL:  internal + "/protocol/openid-connect/logout",
		httpClient: httpClient,
	}
}

// AuthCodeURL implements Authenticator.
func (k *Keycloak) AuthCodeURL(state, nonce, verifier string) string {
	return k.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
}

// Exchange implements Authenticator.
func (k *Keycloak) Exchange(ctx context.Context, code, verifier string) (*Tokens, error) {
	tok, err := k.oauth.Exchange(k.clientCtx(ctx), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}
	return k.verify(ctx, tok)
}

// Refresh implements Authenticator.
func (k *Keycloak) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	// Токен с пустым access и нулевым Expiry считается невалидным — TokenSource сразу делает refresh.
	tok, err := k.oauth.TokenSource(k.clientCtx(ctx), &oauth2.Token{RefreshToken: refreshToken}).Token()
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	return k.verify(ctx, tok)
}

// Logout implements Authenticator (back-channel logout by refresh token).
func (k *Keycloak) Logout(ctx context.Context, refreshToken string) error {
	form := url.Values{"refresh_token": {refreshToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.logoutURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build logout request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(k.oauth.ClientID), url.QueryEscape(k.oauth.ClientSecret))
	resp, err := k.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("logout request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("logout: unexpected status %d", resp.StatusCode)
	}
	return nil
}

type idClaims struct {
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
}

// verify проверяет подписи и claims id_token и access_token, собирает профиль пользователя.
func (k *Keycloak) verify(ctx context.Context, tok *oauth2.Token) (*Tokens, error) {
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return nil, errors.New("id_token is missing in token response")
	}
	idTok, err := k.idVerifier.Verify(ctx, rawID)
	if err != nil {
		return nil, fmt.Errorf("verify id_token: %w", err)
	}
	var c idClaims
	if err := idTok.Claims(&c); err != nil {
		return nil, fmt.Errorf("decode id_token claims: %w", err)
	}
	p, err := k.atVerifier.Verify(ctx, tok.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("verify access_token: %w", err)
	}
	if p.Subject != idTok.Subject {
		return nil, errors.New("access_token and id_token subjects differ")
	}
	return &Tokens{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       tok.Expiry,
		Nonce:        idTok.Nonce,
		User: UserInfo{
			Subject:  idTok.Subject,
			Username: c.PreferredUsername,
			Name:     c.Name,
			Roles:    p.Roles,
		},
	}, nil
}

func (k *Keycloak) clientCtx(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, k.httpClient)
}
