// Package chrepo implements reports.Repository on top of ClickHouse.
package chrepo

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/iudanet/architecture-bionicpro/backend/internal/reports"
)

// Pipeline is the ETL pipeline name whose watermark limits reports.
const Pipeline = "reports_mart"

const watermarkQuery = `
SELECT loaded_until
FROM bionicpro.etl_watermark FINAL
WHERE pipeline = ?
LIMIT 1`

const dailyRowsQuery = `
SELECT user_id, full_name, country, prosthesis_serial, prosthesis_model, date,
       movements, avg_reaction_ms, p95_reaction_ms, max_reaction_ms,
       slow_reactions, errors, avg_battery_pct, min_battery_pct
FROM bionicpro.report_user_daily FINAL
WHERE user_id = ? AND date BETWEEN ? AND ?
ORDER BY prosthesis_serial, date`

// Config holds ClickHouse connection settings.
type Config struct {
	Addr     string
	Database string
	Username string
	Password string
}

// Repo reads the report mart.
type Repo struct {
	conn driver.Conn
}

// Open connects to ClickHouse and checks the connection.
func Open(ctx context.Context, cfg Config) (*Repo, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr:        []string{cfg.Addr},
		Auth:        clickhouse.Auth{Database: cfg.Database, Username: cfg.Username, Password: cfg.Password},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("open clickhouse: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping clickhouse: %w", err)
	}
	return &Repo{conn: conn}, nil
}

// Close closes the connection.
func (r *Repo) Close() error { return r.conn.Close() }

// ProcessedUntil implements reports.Repository.
func (r *Repo) ProcessedUntil(ctx context.Context) (time.Time, bool, error) {
	var rows []struct {
		LoadedUntil time.Time `ch:"loaded_until"`
	}
	if err := r.conn.Select(ctx, &rows, watermarkQuery, Pipeline); err != nil {
		return time.Time{}, false, fmt.Errorf("select watermark: %w", err)
	}
	if len(rows) == 0 {
		return time.Time{}, false, nil
	}
	return rows[0].LoadedUntil, true, nil
}

// DailyRows implements reports.Repository.
func (r *Repo) DailyRows(ctx context.Context, userID string, from, to time.Time) ([]reports.DailyRow, error) {
	var rows []reports.DailyRow
	if err := r.conn.Select(ctx, &rows, dailyRowsQuery, userID, from, to); err != nil {
		return nil, fmt.Errorf("select report rows: %w", err)
	}
	return rows, nil
}
