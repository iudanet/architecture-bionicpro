// Package httpapi exposes the reports service over HTTP.
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/iudanet/architecture-bionicpro/backend/internal/auth"
	"github.com/iudanet/architecture-bionicpro/backend/internal/httpx"
	"github.com/iudanet/architecture-bionicpro/backend/internal/reports"
)

// TokenVerifier validates bearer tokens.
type TokenVerifier interface {
	Verify(ctx context.Context, raw string) (auth.Principal, error)
}

// ReportService builds user reports.
type ReportService interface {
	UserReport(ctx context.Context, userID string, req reports.Request) (reports.Report, error)
}

type principalKey struct{}

// Handler serves /reports.
type Handler struct {
	verifier     TokenVerifier
	service      ReportService
	requiredRole string
	log          *slog.Logger
}

// NewHandler returns the HTTP handler of the reports API.
func NewHandler(verifier TokenVerifier, service ReportService, requiredRole string, log *slog.Logger) http.Handler {
	h := &Handler{verifier: verifier, service: service, requiredRole: requiredRole, log: log}
	mux := http.NewServeMux()
	mux.Handle("GET /reports", h.authenticate(http.HandlerFunc(h.getReport)))
	return mux
}

// authenticate пропускает только запросы с валидным JWT и нужной ролью.
func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || raw == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="reports"`)
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "bearer token is required")
			return
		}
		p, err := h.verifier.Verify(r.Context(), raw)
		if err != nil {
			h.log.WarnContext(r.Context(), "token rejected", "error", err)
			w.Header().Set("WWW-Authenticate", `Bearer realm="reports", error="invalid_token"`)
			httpx.WriteError(w, http.StatusUnauthorized, "invalid_token", "access token is invalid or expired")
			return
		}
		if !p.HasRole(h.requiredRole) {
			h.log.WarnContext(r.Context(), "role missing", "sub", p.Subject, "required_role", h.requiredRole)
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "role "+h.requiredRole+" is required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

func (h *Handler) getReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := ctx.Value(principalKey{}).(auth.Principal)
	q := r.URL.Query()

	// Отчёт всегда строится по sub из токена. Явный запрос чужого пользователя — 403.
	if uid := q.Get("user_id"); uid != "" && uid != p.Subject {
		h.log.WarnContext(ctx, "attempt to read foreign report", "sub", p.Subject, "requested_user_id", uid)
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "you can only access your own report")
		return
	}

	req, err := parseRequest(q.Get("from"), q.Get("to"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	rep, err := h.service.UserReport(ctx, p.Subject, req)
	var npe *reports.NotProcessedError
	switch {
	case err == nil:
		httpx.WriteJSON(w, http.StatusOK, rep)
	case errors.Is(err, reports.ErrInvalidPeriod):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_period", err.Error())
	case errors.As(err, &npe):
		httpx.WriteErrorDetails(w, http.StatusUnprocessableEntity, "period_not_processed", err.Error(),
			map[string]any{"processed_until": npe.ProcessedUntil})
	case errors.Is(err, reports.ErrNotReady):
		w.Header().Set("Retry-After", "600")
		httpx.WriteError(w, http.StatusServiceUnavailable, "report_not_ready", err.Error())
	default:
		h.log.ErrorContext(ctx, "build report", "sub", p.Subject, "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to build report")
	}
}

func parseRequest(from, to string) (reports.Request, error) {
	var req reports.Request
	for _, f := range []struct {
		name, value string
		dst         **time.Time
	}{{"from", from, &req.From}, {"to", to, &req.To}} {
		if f.value == "" {
			continue
		}
		t, err := time.Parse(time.DateOnly, f.value)
		if err != nil {
			return req, errors.New(f.name + " must be a date in YYYY-MM-DD format")
		}
		*f.dst = &t
	}
	return req, nil
}
