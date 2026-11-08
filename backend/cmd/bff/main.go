// Command bff is the Backend-for-Frontend: OAuth 2.0 Authorization Code + PKCE client of Keycloak
// that keeps tokens server-side and proxies API calls of the SPA.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/iudanet/architecture-bionicpro/backend/internal/bff"
	"github.com/iudanet/architecture-bionicpro/backend/internal/env"
	"github.com/iudanet/architecture-bionicpro/backend/internal/httpx"
)

// Session and token timings.
const (
	SessionTTL  = 8 * time.Hour
	LoginTTL    = 10 * time.Minute
	RefreshSkew = 30 * time.Second
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("bff stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	e := env.New()
	addr := e.String("HTTP_ADDR", ":8000")
	kc := bff.KeycloakConfig{
		PublicIssuer:   e.Required("OIDC_PUBLIC_ISSUER"),
		InternalIssuer: e.Required("OIDC_INTERNAL_ISSUER"),
		ClientID:       e.Required("OIDC_CLIENT_ID"),
		ClientSecret:   e.Required("OIDC_CLIENT_SECRET"),
		RedirectURL:    e.Required("OIDC_REDIRECT_URL"),
		APIAudience:    e.Required("OIDC_API_AUDIENCE"),
	}
	reportsURL := e.Required("REPORTS_API_URL")
	cfg := bff.Config{
		FrontendURL:  e.Required("FRONTEND_URL"),
		CookieSecure: e.Bool("COOKIE_SECURE", true),
		SessionTTL:   SessionTTL,
		LoginTTL:     LoginTTL,
		RefreshSkew:  RefreshSkew,
	}
	if err := e.Err(); err != nil {
		return err
	}
	target, err := url.Parse(reportsURL)
	if err != nil {
		return err
	}
	cfg.ReportsURL = target

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	authenticator := bff.NewKeycloak(ctx, kc, &http.Client{Timeout: 10 * time.Second})
	return httpx.Serve(ctx, log, addr, bff.NewServer(cfg, authenticator, log).Handler())
}
