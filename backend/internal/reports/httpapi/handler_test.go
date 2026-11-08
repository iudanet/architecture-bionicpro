package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/iudanet/architecture-bionicpro/backend/internal/auth"
	"github.com/iudanet/architecture-bionicpro/backend/internal/authtest"
	"github.com/iudanet/architecture-bionicpro/backend/internal/reports"
	"github.com/iudanet/architecture-bionicpro/backend/internal/reports/httpapi"
)

type fakeService struct {
	err     error
	gotUser string
	gotReq  reports.Request
	called  bool
}

func (f *fakeService) UserReport(_ context.Context, userID string, req reports.Request) (reports.Report, error) {
	f.called, f.gotUser, f.gotReq = true, userID, req
	if f.err != nil {
		return reports.Report{}, f.err
	}
	return reports.Report{UserID: userID, Prostheses: []reports.ProsthesisReport{}}, nil
}

const role = "prothetic_user"

func setup(t *testing.T, svc *fakeService) (http.Handler, *authtest.TokenIssuer) {
	iss := authtest.New(t)
	verifier := auth.NewVerifier(authtest.Issuer, iss.KeySet(), "reports-api")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httpapi.NewHandler(verifier, svc, role, log), iss
}

func do(h http.Handler, target, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Error.Code
}

func TestReports_Unauthenticated(t *testing.T) {
	svc := &fakeService{}
	h, _ := setup(t, svc)

	rec := do(h, "/reports", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "unauthorized", errorCode(t, rec))

	rec = do(h, "/reports", "not-a-jwt")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "invalid_token", errorCode(t, rec))
	assert.False(t, svc.called)
}

func TestReports_RoleRequired(t *testing.T) {
	svc := &fakeService{}
	h, iss := setup(t, svc)
	rec := do(h, "/reports", iss.AccessToken("u1", "reports-api", "user"))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, svc.called)
}

func TestReports_OwnReportBySubject(t *testing.T) {
	svc := &fakeService{}
	h, iss := setup(t, svc)
	rec := do(h, "/reports?from=2026-11-01&to=2026-11-07", iss.AccessToken("u1", "reports-api", role))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "u1", svc.gotUser)
	require.NotNil(t, svc.gotReq.From)
	assert.Equal(t, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), *svc.gotReq.From)
	assert.Contains(t, rec.Body.String(), `"user_id":"u1"`)
}

func TestReports_ForeignUserForbidden(t *testing.T) {
	svc := &fakeService{}
	h, iss := setup(t, svc)
	token := iss.AccessToken("u1", "reports-api", role)

	rec := do(h, "/reports?user_id=u2", token)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, svc.called)

	rec = do(h, "/reports?user_id=u1", token)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestReports_ServiceErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{&reports.NotProcessedError{ProcessedUntil: time.Now()}, http.StatusUnprocessableEntity, "period_not_processed"},
		{reports.ErrNotReady, http.StatusServiceUnavailable, "report_not_ready"},
		{errors.New("clickhouse is down"), http.StatusInternalServerError, "internal_error"},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			h, iss := setup(t, &fakeService{err: c.err})
			rec := do(h, "/reports", iss.AccessToken("u1", "reports-api", role))
			assert.Equal(t, c.status, rec.Code)
			assert.Equal(t, c.code, errorCode(t, rec))
			assert.NotContains(t, rec.Body.String(), "clickhouse is down", "internal details must not leak")
		})
	}
}
