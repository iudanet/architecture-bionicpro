// Package reports builds prosthesis usage reports from the prepared OLAP mart.
package reports

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Errors returned by Service.
var (
	// ErrInvalidPeriod means the requested period is malformed (from after to, too long).
	ErrInvalidPeriod = errors.New("invalid report period")
	// ErrNotReady means ETL has not processed any data yet.
	ErrNotReady = errors.New("report data is not ready yet")
)

// NotProcessedError means the whole requested period is later than the data processed by ETL.
type NotProcessedError struct {
	ProcessedUntil time.Time
}

func (e *NotProcessedError) Error() string {
	return fmt.Sprintf("requested period is not processed yet, data available until %s", e.ProcessedUntil.Format(time.RFC3339))
}

// DailyRow is one row of the report_user_daily mart.
type DailyRow struct {
	UserID           string    `ch:"user_id"`
	FullName         string    `ch:"full_name"`
	Country          string    `ch:"country"`
	ProsthesisSerial string    `ch:"prosthesis_serial"`
	ProsthesisModel  string    `ch:"prosthesis_model"`
	Date             time.Time `ch:"date"`
	Movements        uint64    `ch:"movements"`
	AvgReactionMs    float64   `ch:"avg_reaction_ms"`
	P95ReactionMs    float64   `ch:"p95_reaction_ms"`
	MaxReactionMs    uint32    `ch:"max_reaction_ms"`
	SlowReactions    uint64    `ch:"slow_reactions"`
	Errors           uint64    `ch:"errors"`
	AvgBatteryPct    float64   `ch:"avg_battery_pct"`
	MinBatteryPct    float32   `ch:"min_battery_pct"`
}

// Repository reads the prepared mart. Implementations must not compute heavy aggregates on the fly.
type Repository interface {
	// ProcessedUntil returns the ETL watermark; ok is false if ETL has never completed.
	ProcessedUntil(ctx context.Context) (t time.Time, ok bool, err error)
	// DailyRows returns mart rows of one user for dates in [from, to], ordered by prosthesis and date.
	DailyRows(ctx context.Context, userID string, from, to time.Time) ([]DailyRow, error)
}

// Request is the requested report period. Nil bounds are filled with defaults.
type Request struct {
	From *time.Time
	To   *time.Time
}

// Period is an inclusive range of dates.
type Period struct {
	From Date `json:"from"`
	To   Date `json:"to"`
}

// Date is a calendar date serialized as YYYY-MM-DD.
type Date time.Time

// MarshalJSON implements json.Marshaler.
func (d Date) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Time(d).Format(time.DateOnly) + `"`), nil
}

// Report is the response for one user.
type Report struct {
	UserID         string             `json:"user_id"`
	FullName       string             `json:"full_name,omitempty"`
	Country        string             `json:"country,omitempty"`
	Period         Period             `json:"period"`
	ProcessedUntil time.Time          `json:"processed_until"`
	Truncated      bool               `json:"truncated"`
	Prostheses     []ProsthesisReport `json:"prostheses"`
}

// ProsthesisReport is the part of a report about one prosthesis.
type ProsthesisReport struct {
	Serial string `json:"serial"`
	Model  string `json:"model"`
	Days   []Day  `json:"days"`
}

// Day is one day of prosthesis usage.
type Day struct {
	Date          Date    `json:"date"`
	Movements     uint64  `json:"movements"`
	AvgReactionMs float64 `json:"avg_reaction_ms"`
	P95ReactionMs float64 `json:"p95_reaction_ms"`
	MaxReactionMs uint32  `json:"max_reaction_ms"`
	SlowReactions uint64  `json:"slow_reactions"`
	Errors        uint64  `json:"errors"`
	AvgBatteryPct float64 `json:"avg_battery_pct"`
	MinBatteryPct float32 `json:"min_battery_pct"`
}

// Service builds reports from the repository.
type Service struct {
	repo        Repository
	defaultDays int
	maxDays     int
}

// NewService creates a report service. defaultDays is the period length when "from" is omitted,
// maxDays limits the period length.
func NewService(repo Repository, defaultDays, maxDays int) *Service {
	return &Service{repo: repo, defaultDays: defaultDays, maxDays: maxDays}
}

// UserReport returns the report of userID, limited to the period already processed by ETL.
func (s *Service) UserReport(ctx context.Context, userID string, req Request) (Report, error) {
	processedUntil, ok, err := s.repo.ProcessedUntil(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("read etl watermark: %w", err)
	}
	if !ok {
		return Report{}, ErrNotReady
	}
	processedUntil = processedUntil.UTC()
	// Последний день, по которому есть хоть какие-то обработанные данные.
	lastDay := truncateDay(processedUntil.Add(-time.Nanosecond))

	to := lastDay
	if req.To != nil {
		to = truncateDay(*req.To)
	}
	from := to.AddDate(0, 0, -(s.defaultDays - 1))
	if req.From != nil {
		from = truncateDay(*req.From)
	}
	// Без явного to период «с будущей даты» — это ещё не обработанные данные, а не ошибка запроса.
	if from.After(to) && req.To != nil {
		return Report{}, fmt.Errorf("%w: from is after to", ErrInvalidPeriod)
	}
	if days := int(to.Sub(from).Hours()/24) + 1; days > s.maxDays {
		return Report{}, fmt.Errorf("%w: period is longer than %d days", ErrInvalidPeriod, s.maxDays)
	}
	// Данных, которых ещё нет в OLAP, не отдаём: период обрезается по watermark.
	if from.After(lastDay) {
		return Report{}, &NotProcessedError{ProcessedUntil: processedUntil}
	}
	truncated := to.After(lastDay)
	if truncated {
		to = lastDay
	}

	rows, err := s.repo.DailyRows(ctx, userID, from, to)
	if err != nil {
		return Report{}, fmt.Errorf("read report rows: %w", err)
	}

	rep := Report{
		UserID:         userID,
		Period:         Period{From: Date(from), To: Date(to)},
		ProcessedUntil: processedUntil,
		Truncated:      truncated,
		Prostheses:     []ProsthesisReport{},
	}
	for _, r := range rows {
		rep.FullName, rep.Country = r.FullName, r.Country
		n := len(rep.Prostheses)
		if n == 0 || rep.Prostheses[n-1].Serial != r.ProsthesisSerial {
			rep.Prostheses = append(rep.Prostheses, ProsthesisReport{Serial: r.ProsthesisSerial, Model: r.ProsthesisModel})
			n++
		}
		p := &rep.Prostheses[n-1]
		p.Days = append(p.Days, Day{
			Date:          Date(truncateDay(r.Date)),
			Movements:     r.Movements,
			AvgReactionMs: r.AvgReactionMs,
			P95ReactionMs: r.P95ReactionMs,
			MaxReactionMs: r.MaxReactionMs,
			SlowReactions: r.SlowReactions,
			Errors:        r.Errors,
			AvgBatteryPct: r.AvgBatteryPct,
			MinBatteryPct: r.MinBatteryPct,
		})
	}
	return rep, nil
}

func truncateDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
