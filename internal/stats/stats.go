package stats

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/platform"
	"github.com/oschina/mothx/internal/session"
)

// StatsEntry represents a single recorded LLM request.
type StatsEntry struct {
	ID           int64     `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	SessionID    string    `json:"sessionId"`
	Vendor       string    `json:"vendor"`
	Protocol     string    `json:"protocol"`
	Model        string    `json:"model"`
	InputTokens  int       `json:"inputTokens"`
	OutputTokens int       `json:"outputTokens"`
	TotalTokens  int       `json:"totalTokens"`
	DurationMs   int       `json:"durationMs"`
}

// Aggregate represents aggregated stats for a dimension.
type Aggregate struct {
	Label        string `json:"label"`
	Vendor       string `json:"vendor"`
	Protocol     string `json:"protocol"`
	Model        string `json:"model"`
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
	TotalTokens  int    `json:"totalTokens"`
	Requests     int    `json:"requests"`
}

// Summary represents overall statistics summary.
type Summary struct {
	TotalRequests int `json:"totalRequests"`
	InputTokens   int `json:"inputTokens"`
	OutputTokens  int `json:"outputTokens"`
	TotalTokens   int `json:"totalTokens"`
}

// Query represents a stats query with filters.
type Query struct {
	From     time.Time
	To       time.Time
	Vendor   string
	Protocol string
	Model    string
	GroupBy  string // "day", "1h", "week", "month", "provider", "model"
}

// DB wraps a SQLite connection for stats queries.
type DB struct {
	// db is retained for backwards-compatible test and integration access to
	// the shared connection. Queries in this package use statsDAO.
	db       *dao.Database
	statsDAO *dao.StatsDAO
}

// Open opens the stats database at the given sessions.db path.
func Open(dbPath string) (*DB, error) {
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("database not found: %s", dbPath)
	}
	db, err := session.OpenBunDatabase(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return &DB{db: db, statsDAO: dao.NewStatsDAO(db.Bun())}, nil
}

// OpenDefault opens the default sessions.db in the user's config directory.
func OpenDefault() (*DB, error) {
	dbPath := filepath.Join(platform.SessionDir(), "sessions.db")
	return Open(dbPath)
}

// Close releases the stats wrapper. The shared session connection is closed by
// session.CloseDatabases during process shutdown.
func (s *DB) Close() error {
	return nil
}

// Summary returns overall summary statistics for the given query.
func (s *DB) Summary(q Query) (*Summary, error) {
	record, err := s.statsDAO.Summary(context.Background(), statsFilter(q))
	if err != nil {
		return nil, err
	}
	return &Summary{TotalRequests: record.TotalRequests, InputTokens: record.InputTokens, OutputTokens: record.OutputTokens, TotalTokens: record.TotalTokens}, nil
}

// TimeSeries returns time-bucketed stats for charting.
func (s *DB) TimeSeries(q Query) ([]Aggregate, error) {
	records, err := s.statsDAO.TimeSeries(context.Background(), statsFilter(q), q.GroupBy)
	if err != nil {
		return nil, err
	}
	results := make([]Aggregate, 0, len(records))
	for _, record := range records {
		results = append(results, Aggregate{Label: record.Label, InputTokens: record.InputTokens, OutputTokens: record.OutputTokens, TotalTokens: record.TotalTokens, Requests: record.Requests})
	}
	return results, nil
}

// ByProvider returns stats grouped by vendor and protocol.
func (s *DB) ByProvider(q Query) ([]Aggregate, error) {
	records, err := s.statsDAO.ByProvider(context.Background(), statsFilter(q))
	if err != nil {
		return nil, err
	}
	results := make([]Aggregate, 0, len(records))
	for _, record := range records {
		results = append(results, Aggregate{Label: record.Vendor, Vendor: record.Vendor, Protocol: record.Protocol, InputTokens: record.InputTokens, OutputTokens: record.OutputTokens, TotalTokens: record.TotalTokens, Requests: record.Requests})
	}
	return results, nil
}

// ByModel returns stats grouped by model.
func (s *DB) ByModel(q Query) ([]Aggregate, error) {
	records, err := s.statsDAO.ByModel(context.Background(), statsFilter(q))
	if err != nil {
		return nil, err
	}
	results := make([]Aggregate, 0, len(records))
	for _, record := range records {
		results = append(results, Aggregate{Label: record.Model, Model: record.Model, Vendor: record.Vendor, Protocol: record.Protocol, InputTokens: record.InputTokens, OutputTokens: record.OutputTokens, TotalTokens: record.TotalTokens, Requests: record.Requests})
	}
	return results, nil
}

// RecentPage represents a paginated result of recent stats entries.
type RecentPage struct {
	Items    []StatsEntry `json:"items"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"pageSize"`
}

// Recent returns a paginated list of stats entries, ordered by most recent first.
func (s *DB) Recent(page, pageSize int) (*RecentPage, error) {
	return s.RecentFiltered(Query{}, page, pageSize)
}

// RecentFiltered returns a paginated list of stats entries matching the query,
// ordered by most recent first.
func (s *DB) RecentFiltered(q Query, page, pageSize int) (*RecentPage, error) {
	if pageSize <= 0 {
		pageSize = 20
	}
	if page <= 0 {
		page = 1
	}

	records, total, err := s.statsDAO.Recent(context.Background(), statsFilter(q), page, pageSize)
	if err != nil {
		return nil, err
	}
	results := make([]StatsEntry, 0, len(records))
	for _, record := range records {
		e := StatsEntry{ID: record.ID, Vendor: record.Provider, Protocol: record.Protocol, Model: record.Model, InputTokens: record.InputTokens, OutputTokens: record.OutputTokens, TotalTokens: record.TotalTokens, DurationMs: record.DurationMs}
		if record.SessionID != nil {
			e.SessionID = *record.SessionID
		}
		e.Timestamp, _ = time.Parse(time.RFC3339Nano, record.Timestamp)
		if e.Timestamp.IsZero() {
			e.Timestamp, _ = time.Parse(time.RFC3339, record.Timestamp)
		}
		results = append(results, e)
	}
	return &RecentPage{Items: results, Total: total, Page: page, PageSize: pageSize}, nil
}

func statsFilter(q Query) dao.StatsFilter {
	filter := dao.StatsFilter{Provider: q.Vendor, Protocol: q.Protocol, Model: q.Model}
	if !q.From.IsZero() {
		filter.From = q.From.Format(time.RFC3339Nano)
	}
	if !q.To.IsZero() {
		filter.To = q.To.Format(time.RFC3339Nano)
	}
	return filter
}
