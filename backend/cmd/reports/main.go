// Command reports serves GET /reports: prosthesis usage reports read from the ClickHouse mart.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/iudanet/architecture-bionicpro/backend/internal/auth"
	"github.com/iudanet/architecture-bionicpro/backend/internal/env"
	"github.com/iudanet/architecture-bionicpro/backend/internal/httpx"
	"github.com/iudanet/architecture-bionicpro/backend/internal/reports"
	"github.com/iudanet/architecture-bionicpro/backend/internal/reports/chrepo"
	"github.com/iudanet/architecture-bionicpro/backend/internal/reports/httpapi"
)

// Report period limits, in days.
const (
	DefaultReportDays = 30
	MaxReportDays     = 366
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("reports api stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	e := env.New()
	addr := e.String("HTTP_ADDR", ":8001")
	issuer := e.Required("OIDC_ISSUER")
	jwksURL := e.Required("OIDC_JWKS_URL")
	audience := e.Required("OIDC_AUDIENCE")
	role := e.Required("REQUIRED_ROLE")
	chCfg := chrepo.Config{
		Addr:     e.Required("CLICKHOUSE_ADDR"),
		Database: e.String("CLICKHOUSE_DB", "bionicpro"),
		Username: e.Required("CLICKHOUSE_USER"),
		Password: e.Required("CLICKHOUSE_PASSWORD"),
	}
	if err := e.Err(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	repo, err := chrepo.Open(ctx, chCfg)
	if err != nil {
		return err
	}
	defer repo.Close()

	verifier := auth.NewRemoteVerifier(ctx, issuer, jwksURL, audience)
	service := reports.NewService(repo, DefaultReportDays, MaxReportDays)
	return httpx.Serve(ctx, log, addr, httpapi.NewHandler(verifier, service, role, log))
}
