package bff

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const frontendURL = "http://localhost:3000/"

type upstreamCall struct {
	path, query, authorization, cookie string
}

type harness struct {
	t        *testing.T
	auth     *AuthenticatorMock
	server   *Server
	handler  http.Handler
	upstream *[]upstreamCall
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	calls := &[]upstreamCall{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, upstreamCall{
			path: r.URL.Path, query: r.URL.RawQuery,
			authorization: r.Header.Get("Authorization"), cookie: r.Header.Get("Cookie"),
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"user_id":"u1"}`)
	}))
	t.Cleanup(upstream.Close)
	target, err := url.Parse(upstream.URL)
	require.NoError(t, err)

	mock := &AuthenticatorMock{
		AuthCodeURLFunc: func(state, nonce, verifier string) string {
			return "http://idp.test/auth?state=" + state
		},
		LogoutFunc: func(context.Context, string) error { return nil },
	}
	srv := NewServer(Config{
		FrontendURL: frontendURL,
		ReportsURL:  target,
		SessionTTL:  time.Hour,
		LoginTTL:    time.Minute,
		RefreshSkew: 30 * time.Second,
	}, mock, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &harness{t: t, auth: mock, server: srv, handler: srv.Handler(), upstream: calls}
}

func (h *harness) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func cookieFrom(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func validTokens(nonce string) *Tokens {
	return &Tokens{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		Expiry:       time.Now().Add(5 * time.Minute),
		Nonce:        nonce,
		User:         UserInfo{Subject: "u1", Username: "prothetic1", Roles: []string{"prothetic_user"}},
	}
}

// login проходит /auth/login и /auth/callback и возвращает cookie сессии.
func (h *harness) login() *http.Cookie {
	h.t.Helper()
	rec := h.do(httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	require.Equal(h.t, http.StatusFound, rec.Code)
	loginCookie := cookieFrom(rec, LoginCookie)
	require.NotNil(h.t, loginCookie)

	call := h.auth.AuthCodeURLCalls()[len(h.auth.AuthCodeURLCalls())-1]
	h.auth.ExchangeFunc = func(_ context.Context, code, verifier string) (*Tokens, error) {
		assert.Equal(h.t, "the-code", code)
		assert.Equal(h.t, call.Verifier, verifier, "BFF must send the PKCE verifier generated at login")
		return validTokens(call.Nonce), nil
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=the-code&state="+call.State, nil)
	req.AddCookie(loginCookie)
	rec = h.do(req)
	require.Equal(h.t, http.StatusFound, rec.Code, rec.Body.String())
	assert.Equal(h.t, frontendURL, rec.Header().Get("Location"))
	sess := cookieFrom(rec, SessionCookie)
	require.NotNil(h.t, sess)
	return sess
}

func TestLogin_RedirectsToIdPWithPKCE(t *testing.T) {
	h := newHarness(t)
	rec := h.do(httptest.NewRequest(http.MethodGet, "/auth/login", nil))

	require.Equal(t, http.StatusFound, rec.Code)
	assert.True(t, strings.HasPrefix(rec.Header().Get("Location"), "http://idp.test/auth"))

	require.Len(t, h.auth.AuthCodeURLCalls(), 1)
	call := h.auth.AuthCodeURLCalls()[0]
	assert.NotEmpty(t, call.State)
	assert.NotEmpty(t, call.Nonce)
	assert.GreaterOrEqual(t, len(call.Verifier), 43, "RFC 7636: verifier is 43..128 chars")

	c := cookieFrom(rec, LoginCookie)
	require.NotNil(t, c)
	assert.Equal(t, call.State, c.Value)
	assert.True(t, c.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
}

func TestCallback_CreatesSessionWithoutExposingTokens(t *testing.T) {
	h := newHarness(t)
	sess := h.login()

	assert.True(t, sess.HttpOnly, "session cookie must be HttpOnly")
	assert.NotContains(t, sess.Value, "access-1")

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(sess)
	rec := h.do(req)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `"username":"prothetic1"`)
	for _, secret := range []string{"access-1", "refresh-1"} {
		assert.NotContains(t, body, secret, "tokens must never reach the frontend")
	}
}

func TestCallback_State(t *testing.T) {
	t.Run("missing or foreign cookie", func(t *testing.T) {
		h := newHarness(t)
		h.do(httptest.NewRequest(http.MethodGet, "/auth/login", nil))
		state := h.auth.AuthCodeURLCalls()[0].State

		rec := h.do(httptest.NewRequest(http.MethodGet, "/auth/callback?code=c&state="+state, nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code)

		req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=c&state="+state, nil)
		req.AddCookie(&http.Cookie{Name: LoginCookie, Value: "other-state"})
		assert.Equal(t, http.StatusBadRequest, h.do(req).Code)
		assert.Empty(t, h.auth.ExchangeCalls())
	})

	t.Run("replay", func(t *testing.T) {
		h := newHarness(t)
		h.login()
		state := h.auth.AuthCodeURLCalls()[0].State

		req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=the-code&state="+state, nil)
		req.AddCookie(&http.Cookie{Name: LoginCookie, Value: state})
		assert.Equal(t, http.StatusBadRequest, h.do(req).Code)
		assert.Len(t, h.auth.ExchangeCalls(), 1)
	})
}

func TestCallback_NonceMismatch(t *testing.T) {
	h := newHarness(t)
	rec := h.do(httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	call := h.auth.AuthCodeURLCalls()[0]
	h.auth.ExchangeFunc = func(context.Context, string, string) (*Tokens, error) {
		return validTokens("forged-nonce"), nil
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=c&state="+call.State, nil)
	req.AddCookie(cookieFrom(rec, LoginCookie))
	rec = h.do(req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Nil(t, cookieFrom(rec, SessionCookie))
}

func apiRequest(sess *http.Cookie, csrf bool) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/reports?from=2026-11-01", nil)
	if sess != nil {
		req.AddCookie(sess)
	}
	if csrf {
		req.Header.Set(CSRFHeader, "fetch")
	}
	return req
}

func TestAPI_ProxiesWithBearerAndWithoutCookie(t *testing.T) {
	h := newHarness(t)
	sess := h.login()

	rec := h.do(apiRequest(sess, true))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"user_id":"u1"}`, rec.Body.String())

	require.Len(t, *h.upstream, 1)
	got := (*h.upstream)[0]
	assert.Equal(t, "/reports", got.path)
	assert.Equal(t, "from=2026-11-01", got.query)
	assert.Equal(t, "Bearer access-1", got.authorization)
	assert.Empty(t, got.cookie, "session cookie must not be forwarded")
}

func TestAPI_RequiresSessionAndCSRFHeader(t *testing.T) {
	h := newHarness(t)
	sess := h.login()

	assert.Equal(t, http.StatusUnauthorized, h.do(apiRequest(nil, true)).Code)
	assert.Equal(t, http.StatusForbidden, h.do(apiRequest(sess, false)).Code)

	forged := &http.Cookie{Name: SessionCookie, Value: "guessed"}
	assert.Equal(t, http.StatusUnauthorized, h.do(apiRequest(forged, true)).Code)
	assert.Empty(t, *h.upstream)
}

func TestAPI_Refresh(t *testing.T) {
	t.Run("expiring token is refreshed", func(t *testing.T) {
		h := newHarness(t)
		sess := h.login()
		h.server.now = func() time.Time { return time.Now().Add(4*time.Minute + 45*time.Second) }
		h.auth.RefreshFunc = func(_ context.Context, refreshToken string) (*Tokens, error) {
			assert.Equal(t, "refresh-1", refreshToken)
			tok := validTokens("")
			tok.AccessToken, tok.RefreshToken = "access-2", ""
			tok.Expiry = time.Now().Add(15 * time.Minute)
			return tok, nil
		}

		require.Equal(t, http.StatusOK, h.do(apiRequest(sess, true)).Code)
		assert.Equal(t, "Bearer access-2", (*h.upstream)[0].authorization)
		stored, ok := h.server.sessions.Get(sess.Value)
		require.True(t, ok)
		assert.Equal(t, "refresh-1", stored.Tokens.RefreshToken, "refresh token is kept if IdP did not rotate it")
	})

	t.Run("failed refresh closes session", func(t *testing.T) {
		h := newHarness(t)
		sess := h.login()
		h.server.now = func() time.Time { return time.Now().Add(time.Hour) }
		h.auth.RefreshFunc = func(context.Context, string) (*Tokens, error) {
			return nil, errors.New("invalid_grant")
		}

		assert.Equal(t, http.StatusUnauthorized, h.do(apiRequest(sess, true)).Code)
		_, ok := h.server.sessions.Get(sess.Value)
		assert.False(t, ok)
		assert.Empty(t, *h.upstream)
	})
}

func TestLogout(t *testing.T) {
	h := newHarness(t)
	sess := h.login()

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(sess)
	assert.Equal(t, http.StatusForbidden, h.do(req).Code, "logout requires CSRF header")

	req.Header.Set(CSRFHeader, "fetch")
	rec := h.do(req)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	require.Len(t, h.auth.LogoutCalls(), 1)
	assert.Equal(t, "refresh-1", h.auth.LogoutCalls()[0].RefreshToken)

	me := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	me.AddCookie(sess)
	assert.Equal(t, http.StatusUnauthorized, h.do(me).Code)
}
