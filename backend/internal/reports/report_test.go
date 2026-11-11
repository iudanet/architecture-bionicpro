package reports_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/iudanet/architecture-bionicpro/backend/internal/reports"
)

type fakeRepo struct {
	processedUntil time.Time
	ready          bool
	rows           []reports.DailyRow
	err            error

	gotUser     string
	gotFrom     time.Time
	gotTo       time.Time
	rowsQueried bool
}

func (f *fakeRepo) ProcessedUntil(context.Context) (time.Time, bool, error) {
	return f.processedUntil, f.ready, f.err
}

func (f *fakeRepo) DailyRows(_ context.Context, userID string, from, to time.Time) ([]reports.DailyRow, error) {
	f.rowsQueried = true
	f.gotUser, f.gotFrom, f.gotTo = userID, from, to
	return f.rows, nil
}

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr(t time.Time) *time.Time { return &t }

// watermark: 2026-11-08 14:00 UTC — последний (частично) обработанный день 2026-11-08.
var watermark = time.Date(2026, 11, 8, 14, 0, 0, 0, time.UTC)

func TestUserReport_NotReady(t *testing.T) {
	svc := reports.NewService(&fakeRepo{}, 30, 366)
	_, err := svc.UserReport(context.Background(), "u1", reports.Request{})
	assert.ErrorIs(t, err, reports.ErrNotReady)
}

func TestUserReport_DefaultPeriodEndsAtWatermark(t *testing.T) {
	repo := &fakeRepo{processedUntil: watermark, ready: true}
	rep, err := reports.NewService(repo, 30, 366).UserReport(context.Background(), "u1", reports.Request{})
	require.NoError(t, err)

	assert.Equal(t, "u1", repo.gotUser)
	assert.Equal(t, day("2026-10-10"), repo.gotFrom)
	assert.Equal(t, day("2026-11-08"), repo.gotTo)
	assert.False(t, rep.Truncated)
	assert.Equal(t, watermark, rep.ProcessedUntil)
	assert.Empty(t, rep.Prostheses)

	t.Run("watermark at midnight excludes that day", func(t *testing.T) {
		repo := &fakeRepo{processedUntil: day("2026-11-08"), ready: true}
		_, err := reports.NewService(repo, 1, 366).UserReport(context.Background(), "u1", reports.Request{})
		require.NoError(t, err)
		assert.Equal(t, day("2026-11-07"), repo.gotTo)
	})
}

func TestUserReport_PeriodAfterWatermarkIsTruncated(t *testing.T) {
	repo := &fakeRepo{processedUntil: watermark, ready: true}
	rep, err := reports.NewService(repo, 30, 366).UserReport(context.Background(), "u1", reports.Request{
		From: ptr(day("2026-11-01")),
		To:   ptr(day("2026-11-30")),
	})
	require.NoError(t, err)
	assert.True(t, rep.Truncated)
	assert.Equal(t, day("2026-11-08"), repo.gotTo)
	assert.Equal(t, reports.Date(day("2026-11-08")), rep.Period.To)
}

func TestUserReport_WholePeriodNotProcessed(t *testing.T) {
	repo := &fakeRepo{processedUntil: watermark, ready: true}
	_, err := reports.NewService(repo, 30, 366).UserReport(context.Background(), "u1", reports.Request{
		From: ptr(day("2026-11-09")),
		To:   ptr(day("2026-11-10")),
	})
	var npe *reports.NotProcessedError
	require.ErrorAs(t, err, &npe)
	assert.Equal(t, watermark, npe.ProcessedUntil)
	assert.False(t, repo.rowsQueried, "OLAP must not be queried for unprocessed period")

	_, err = reports.NewService(repo, 30, 366).UserReport(context.Background(), "u1", reports.Request{
		From: ptr(day("2026-11-20")),
	})
	require.ErrorAs(t, err, &npe)
}

func TestUserReport_InvalidPeriod(t *testing.T) {
	repo := &fakeRepo{processedUntil: watermark, ready: true}
	svc := reports.NewService(repo, 30, 31)

	_, err := svc.UserReport(context.Background(), "u1", reports.Request{From: ptr(day("2026-11-05")), To: ptr(day("2026-11-01"))})
	assert.ErrorIs(t, err, reports.ErrInvalidPeriod)

	_, err = svc.UserReport(context.Background(), "u1", reports.Request{From: ptr(day("2026-01-01")), To: ptr(day("2026-11-01"))})
	assert.ErrorIs(t, err, reports.ErrInvalidPeriod)
}

func TestUserReport_GroupsByProsthesis(t *testing.T) {
	repo := &fakeRepo{processedUntil: watermark, ready: true, rows: []reports.DailyRow{
		{FullName: "Мария", ProsthesisSerial: "ARM", ProsthesisModel: "Arm X2", Date: day("2026-11-07"), Movements: 10},
		{FullName: "Мария", ProsthesisSerial: "ARM", ProsthesisModel: "Arm X2", Date: day("2026-11-08"), Movements: 30},
		{FullName: "Мария", ProsthesisSerial: "LEG", ProsthesisModel: "Leg K1", Date: day("2026-11-08"), Movements: 5},
	}}
	rep, err := reports.NewService(repo, 30, 366).UserReport(context.Background(), "u1", reports.Request{})
	require.NoError(t, err)

	assert.Equal(t, "Мария", rep.FullName)
	require.Len(t, rep.Prostheses, 2)
	assert.Equal(t, "ARM", rep.Prostheses[0].Serial)
	assert.Len(t, rep.Prostheses[0].Days, 2)
	assert.Equal(t, "LEG", rep.Prostheses[1].Serial)
	assert.Len(t, rep.Prostheses[1].Days, 1)
}
