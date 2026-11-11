package bff

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/iudanet/architecture-bionicpro/backend/internal/httpx"
)

//go:generate moq -out authenticator_mock_test.go . Authenticator

// Cookie names.
const (
	SessionCookie = "bionicpro_session"
	LoginCookie   = "bionicpro_login"
	// CSRFHeader is required on API and state-changing requests: it cannot be set cross-site without a CORS preflight.
	CSRFHeader = "X-Requested-With"
)

// Config configures the BFF server.
type Config struct {
	// FrontendURL is where the browser is redirected after login.
	FrontendURL string
	// ReportsURL is the internal URL of the reports API.
	ReportsURL *url.URL
	// CookieSecure sets the Secure flag (must be true in production behind HTTPS).
	CookieSecure bool
	// SessionTTL is the absolute session lifetime.
	SessionTTL time.Duration
	// LoginTTL is how long a started login may take.
	LoginTTL time.Duration
	// RefreshSkew refreshes the access token this long before it expires.
	RefreshSkew time.Duration
}

type pendingLogin struct {
	Nonce    string
	Verifier string
}

// Session is a server-side user session.
type Session struct {
	Tokens Tokens
}

type tokenKey struct{}

// Server is the BFF HTTP server.
type Server struct {
	cfg      Config
	auth     Authenticator
	sessions *TTLStore[Session]
	logins   *TTLStore[pendingLogin]
	proxy    *httputil.ReverseProxy
	log      *slog.Logger
	now      func() time.Time
}

// NewServer creates the BFF.
func NewServer(cfg Config, authenticator Authenticator, log *slog.Logger) *Server {
	s := &Server{
		cfg:      cfg,
		auth:     authenticator,
		sessions: NewTTLStore[Session](),
		logins:   NewTTLStore[pendingLogin](),
		log:      log,
		now:      time.Now,
	}
	s.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(cfg.ReportsURL)
			// /api/reports -> /reports
			pr.Out.URL.Path = strings.TrimSuffix(cfg.ReportsURL.Path, "/") + strings.TrimPrefix(pr.In.URL.Path, "/api")
			pr.Out.URL.RawPath = ""
			// Cookie сессии дальше BFF не уходит; вместо неё — access token.
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Set("Authorization", "Bearer "+pr.In.Context().Value(tokenKey{}).(string))
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.ErrorContext(r.Context(), "proxy to reports api", "error", err)
			httpx.WriteError(w, http.StatusBadGateway, "upstream_unavailable", "reports service is unavailable")
		},
	}
	return s
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/login", s.login)
	mux.HandleFunc("GET /auth/callback", s.callback)
	mux.HandleFunc("GET /auth/me", s.me)
	mux.Handle("POST /auth/logout", s.requireCSRFHeader(http.HandlerFunc(s.logout)))
	mux.Handle("/api/", s.requireCSRFHeader(s.requireSession(s.proxy)))
	return mux
}

// login начинает Authorization Code Flow с PKCE: state и nonce — против CSRF и replay,
// code_verifier остаётся на сервере, в IdP уходит только его S256-хэш.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	state, nonce := randomString(), randomString()
	verifier := oauth2.GenerateVerifier()
	s.logins.Put(state, pendingLogin{Nonce: nonce, Verifier: verifier}, s.cfg.LoginTTL)
	s.setCookie(w, LoginCookie, state, s.cfg.LoginTTL)
	http.Redirect(w, r, s.auth.AuthCodeURL(state, nonce, verifier), http.StatusFound)
}

func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	s.clearCookie(w, LoginCookie)

	if e := q.Get("error"); e != "" {
		s.log.WarnContext(ctx, "idp returned error", "error", e, "description", q.Get("error_description"))
		s.redirectToFrontend(w, r, "login_failed")
		return
	}
	state := q.Get("state")
	c, err := r.Cookie(LoginCookie)
	if state == "" || err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1 {
		s.log.WarnContext(ctx, "state mismatch on callback")
		httpx.WriteError(w, http.StatusBadRequest, "invalid_state", "login state is missing or does not match")
		return
	}
	pending, ok := s.logins.Take(state)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_state", "login has expired, start again")
		return
	}

	tokens, err := s.auth.Exchange(ctx, q.Get("code"), pending.Verifier)
	if err != nil {
		s.log.ErrorContext(ctx, "code exchange failed", "error", err)
		s.redirectToFrontend(w, r, "login_failed")
		return
	}
	if subtle.ConstantTimeCompare([]byte(tokens.Nonce), []byte(pending.Nonce)) != 1 {
		s.log.WarnContext(ctx, "nonce mismatch", "sub", tokens.User.Subject)
		httpx.WriteError(w, http.StatusBadRequest, "invalid_nonce", "id token nonce does not match")
		return
	}

	// Новый идентификатор сессии на каждый вход (защита от session fixation).
	sid := randomString()
	s.sessions.Put(sid, Session{Tokens: *tokens}, s.cfg.SessionTTL)
	s.setCookie(w, SessionCookie, sid, s.cfg.SessionTTL)
	s.log.InfoContext(ctx, "user logged in", "sub", tokens.User.Subject, "username", tokens.User.Username)
	s.redirectToFrontend(w, r, "")
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := s.session(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthenticated", "login is required")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sess.Tokens.User)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	sess, sid, ok := s.session(r)
	if ok {
		s.sessions.Delete(sid)
		if err := s.auth.Logout(r.Context(), sess.Tokens.RefreshToken); err != nil {
			s.log.ErrorContext(r.Context(), "idp logout failed", "sub", sess.Tokens.User.Subject, "error", err)
		}
	}
	s.clearCookie(w, SessionCookie)
	w.WriteHeader(http.StatusNoContent)
}

// requireSession находит сессию, при необходимости обновляет access token и кладёт его в контекст для прокси.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		sess, sid, ok := s.session(r)
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthenticated", "login is required")
			return
		}
		if s.now().Add(s.cfg.RefreshSkew).After(sess.Tokens.Expiry) {
			fresh, err := s.auth.Refresh(ctx, sess.Tokens.RefreshToken)
			if err != nil {
				s.log.WarnContext(ctx, "token refresh failed, session closed", "sub", sess.Tokens.User.Subject, "error", err)
				s.sessions.Delete(sid)
				s.clearCookie(w, SessionCookie)
				httpx.WriteError(w, http.StatusUnauthorized, "session_expired", "session has expired, login again")
				return
			}
			if fresh.RefreshToken == "" {
				fresh.RefreshToken = sess.Tokens.RefreshToken
			}
			sess.Tokens = *fresh
			s.sessions.Update(sid, sess)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, tokenKey{}, sess.Tokens.AccessToken)))
	})
}

func (s *Server) requireCSRFHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(CSRFHeader) == "" {
			httpx.WriteError(w, http.StatusForbidden, "csrf", CSRFHeader+" header is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) session(r *http.Request) (Session, string, bool) {
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return Session{}, "", false
	}
	sess, ok := s.sessions.Get(c.Value)
	return sess, c.Value, ok
}

func (s *Server) redirectToFrontend(w http.ResponseWriter, r *http.Request, loginError string) {
	target := s.cfg.FrontendURL
	if loginError != "" {
		target += "?login_error=" + url.QueryEscape(loginError)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// randomString returns 256 bits of randomness, base64url-encoded.
func randomString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	return base64.RawURLEncoding.EncodeToString(b)
}
