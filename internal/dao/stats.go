package dao

import (
	"context"

	"github.com/uptrace/bun"
)

// StatsFilter contains the optional predicates shared by all stats queries.
// Timestamps are stored as RFC3339 strings in SQLite, so lexical comparison
// preserves chronological ordering.
type StatsFilter struct {
	From     string
	To       string
	Provider string
	Protocol string
	Model    string
}

type statsTable struct {
	bun.BaseModel `bun:"table:request_stats"`
}

// StatsSummaryRecord is the aggregate result returned by Summary.
type StatsSummaryRecord struct {
	statsTable
	TotalRequests int `bun:"total_requests"`
	InputTokens   int `bun:"input_tokens"`
	OutputTokens  int `bun:"output_tokens"`
	TotalTokens   int `bun:"total_tokens"`
}

// StatsAggregateRecord is the aggregate result returned by grouped queries.
type StatsAggregateRecord struct {
	statsTable
	Label        string `bun:"label"`
	Vendor       string `bun:"vendor"`
	Protocol     string `bun:"protocol"`
	Model        string `bun:"model"`
	InputTokens  int    `bun:"input_tokens"`
	OutputTokens int    `bun:"output_tokens"`
	TotalTokens  int    `bun:"total_tokens"`
	Requests     int    `bun:"requests"`
}

// StatsRecord is the persisted request_stats row.
type StatsRecord struct {
	statsTable
	ID           int64   `bun:"id,pk"`
	Timestamp    string  `bun:"timestamp"`
	SessionID    *string `bun:"session_id"`
	Provider     string  `bun:"provider"`
	Protocol     string  `bun:"protocol"`
	Model        string  `bun:"model"`
	InputTokens  int     `bun:"input_tokens"`
	OutputTokens int     `bun:"output_tokens"`
	TotalTokens  int     `bun:"total_tokens"`
	DurationMs   int     `bun:"duration_ms"`
}

// ProviderUsageRecord is the per-provider usage aggregate returned by
// ProviderUsage.
type ProviderUsageRecord struct {
	Provider   string `bun:"provider"`
	Requests   int    `bun:"requests"`
	LastUsedAt string `bun:"last_used_at"`
}

// ProviderUsage aggregates request_stats per provider: how many requests it
// served and when it was last used. It backs the Desktop-facing provider pick
// order from recorded usage instead of a second usage table.
func (d *StatsDAO) ProviderUsage(ctx context.Context, filter StatsFilter) ([]ProviderUsageRecord, error) {
	var records []ProviderUsageRecord
	query := d.db.NewSelect().Model(&records).ModelTableExpr("request_stats").
		ColumnExpr("provider, COUNT(*) AS requests, MAX(timestamp) AS last_used_at").
		Group("provider").OrderExpr("MAX(timestamp) DESC, requests DESC")
	applyStatsFilter(query, filter)
	err := query.Scan(ctx)
	return records, err
}

// StatsDAO provides Bun-backed access to request_stats.
type StatsDAO struct {
	db *bun.DB
}

func NewStatsDAO(db *bun.DB) *StatsDAO {
	return &StatsDAO{db: db}
}

// Insert records one provider request. Callers that already hold a larger
// transaction should use the supplied Bun transaction so usage and lease
// validation remain atomic.
func (d *StatsDAO) Insert(ctx context.Context, tx bun.Tx, record *StatsRecord) error {
	if record == nil {
		return nil
	}
	_, err := tx.NewInsert().Model(record).ModelTableExpr("request_stats").ExcludeColumn("id").Exec(ctx)
	return err
}

func (d *StatsDAO) Summary(ctx context.Context, filter StatsFilter) (StatsSummaryRecord, error) {
	var record StatsSummaryRecord
	query := d.db.NewSelect().Model(&record).ModelTableExpr("request_stats").ColumnExpr("COUNT(*) AS total_requests, CAST(COALESCE(SUM(input_tokens), 0) AS INTEGER) AS input_tokens, CAST(COALESCE(SUM(output_tokens), 0) AS INTEGER) AS output_tokens, CAST(COALESCE(SUM(total_tokens), 0) AS INTEGER) AS total_tokens")
	applyStatsFilter(query, filter)
	err := query.Scan(ctx)
	return record, err
}

func (d *StatsDAO) TimeSeries(ctx context.Context, filter StatsFilter, groupBy string) ([]StatsAggregateRecord, error) {
	bucket := statsBucketExpr(groupBy)
	var records []StatsAggregateRecord
	query := d.db.NewSelect().Model(&records).ModelTableExpr("request_stats").ColumnExpr(bucket + " AS label, CAST(COALESCE(SUM(input_tokens), 0) AS INTEGER) AS input_tokens, CAST(COALESCE(SUM(output_tokens), 0) AS INTEGER) AS output_tokens, CAST(COALESCE(SUM(total_tokens), 0) AS INTEGER) AS total_tokens, COUNT(*) AS requests").GroupExpr("label").OrderExpr("label")
	applyStatsFilter(query, filter)
	err := query.Scan(ctx)
	return records, err
}

func (d *StatsDAO) ByProvider(ctx context.Context, filter StatsFilter) ([]StatsAggregateRecord, error) {
	var records []StatsAggregateRecord
	query := d.db.NewSelect().Model(&records).ModelTableExpr("request_stats").ColumnExpr("provider AS vendor, protocol, CAST(COALESCE(SUM(input_tokens), 0) AS INTEGER) AS input_tokens, CAST(COALESCE(SUM(output_tokens), 0) AS INTEGER) AS output_tokens, CAST(COALESCE(SUM(total_tokens), 0) AS INTEGER) AS total_tokens, COUNT(*) AS requests").Group("provider", "protocol").OrderExpr("total_tokens DESC")
	applyStatsFilter(query, filter)
	err := query.Scan(ctx)
	return records, err
}

func (d *StatsDAO) ByModel(ctx context.Context, filter StatsFilter) ([]StatsAggregateRecord, error) {
	var records []StatsAggregateRecord
	query := d.db.NewSelect().Model(&records).ModelTableExpr("request_stats").ColumnExpr("model, provider AS vendor, protocol, CAST(COALESCE(SUM(input_tokens), 0) AS INTEGER) AS input_tokens, CAST(COALESCE(SUM(output_tokens), 0) AS INTEGER) AS output_tokens, CAST(COALESCE(SUM(total_tokens), 0) AS INTEGER) AS total_tokens, COUNT(*) AS requests").Group("model", "provider", "protocol").OrderExpr("total_tokens DESC")
	applyStatsFilter(query, filter)
	err := query.Scan(ctx)
	return records, err
}

// Recent returns rows and the total number of matching rows.
func (d *StatsDAO) Recent(ctx context.Context, filter StatsFilter, page, pageSize int) ([]StatsRecord, int, error) {
	countQuery := d.db.NewSelect().Model((*StatsRecord)(nil)).ModelTableExpr("request_stats")
	applyStatsFilter(countQuery, filter)
	total, err := countQuery.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	var records []StatsRecord
	query := d.db.NewSelect().Model(&records).ModelTableExpr("request_stats").ColumnExpr("id, timestamp, session_id, provider, protocol, model, input_tokens, output_tokens, total_tokens, duration_ms").OrderExpr("id DESC").Limit(pageSize).Offset((page - 1) * pageSize)
	applyStatsFilter(query, filter)
	if err := query.Scan(ctx); err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

func applyStatsFilter(query *bun.SelectQuery, filter StatsFilter) {
	if filter.From != "" {
		query.Where("timestamp >= ?", filter.From)
	}
	if filter.To != "" {
		query.Where("timestamp < ?", filter.To)
	}
	if filter.Provider != "" {
		query.Where("provider = ?", filter.Provider)
	}
	if filter.Protocol != "" {
		query.Where("protocol = ?", filter.Protocol)
	}
	if filter.Model != "" {
		query.Where("model = ?", filter.Model)
	}
}

func statsBucketExpr(groupBy string) string {
	switch groupBy {
	case "1h":
		return "substr(timestamp, 1, 10) || ' ' || substr(timestamp, 12, 2) || ':00'"
	case "week":
		return "substr(timestamp, 1, 4) || '-W' || substr(timestamp, 6, 2) || '-' || substr(timestamp, 9, 2)"
	case "month":
		return "substr(timestamp, 1, 7)"
	default:
		return "substr(timestamp, 1, 10)"
	}
}
