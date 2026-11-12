package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/angkaberapa/AAT/services/bnpb-satellite/internal/domain"
)

const schema = `
CREATE TABLE IF NOT EXISTS state (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS detections (
	detection_key TEXT PRIMARY KEY,
	satellite TEXT NOT NULL,
	acq_date TEXT NOT NULL,
	acq_time TEXT NOT NULL,
	latitude TEXT NOT NULL,
	longitude TEXT NOT NULL,
	confidence TEXT NOT NULL,
	frp REAL,
	observed_at TEXT NOT NULL,
	active INTEGER NOT NULL DEFAULT 1,
	raw_json TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS detections_time ON detections(observed_at);
CREATE INDEX IF NOT EXISTS detections_active ON detections(active, observed_at);

CREATE TABLE IF NOT EXISTS fires (
	fire_id TEXT PRIMARY KEY,
	source_ref_id TEXT NOT NULL UNIQUE,
	event_json BLOB NOT NULL,
	version INTEGER NOT NULL DEFAULT 1,
	first_detection_key TEXT NOT NULL,
	last_detection_at TEXT NOT NULL,
	detection_count INTEGER NOT NULL,
	total_frp REAL NOT NULL,
	active INTEGER NOT NULL DEFAULT 1,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS outbox (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	producer_event_id TEXT NOT NULL UNIQUE,
	operation TEXT NOT NULL,
	source_position TEXT NOT NULL,
	payload BLOB NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	next_attempt_at TEXT NOT NULL,
	last_error TEXT,
	sent_at TEXT,
	parked_at TEXT,
	park_reason TEXT
);
CREATE INDEX IF NOT EXISTS outbox_pending ON outbox(sent_at, parked_at, next_attempt_at, id);
`

type Store struct {
	db *sql.DB
}

type OutboxItem struct {
	ID              int64
	ProducerEventID string
	Operation       string
	SourcePosition  string
	Payload         []byte
	Attempts        int
	NextAttemptAt   time.Time
	LastError       string
}

type Stats struct {
	OutboxPending int    `json:"outbox_pending"`
	OutboxParked  int    `json:"outbox_parked"`
	Detections    int    `json:"detections"`
	ActiveFires   int    `json:"active_fires"`
	Offset        string `json:"offset"`
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	query := url.Values{}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(NORMAL)")
	query.Add("_pragma", "foreign_keys(1)")
	dbPath := strings.ReplaceAll(filepath.ToSlash(absolute), " ", "%20")
	dsn := "file:" + dbPath + "?" + query.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (s *Store) Offset(ctx context.Context) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM state WHERE key = 'sse_offset'`).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read offset: %w", err)
	}
	return value, nil
}

func SetOffsetTx(ctx context.Context, tx *sql.Tx, offset string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO state(key, value) VALUES('sse_offset', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, offset)
	if err != nil {
		return fmt.Errorf("save offset: %w", err)
	}
	return nil
}

func InsertDetectionTx(ctx context.Context, tx *sql.Tx, detection domain.Detection) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO detections(
			detection_key, satellite, acq_date, acq_time, latitude, longitude,
			confidence, frp, observed_at, active, raw_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(detection_key) DO NOTHING`,
		detection.Key, detection.Satellite, detection.AcqDate, detection.AcqTime,
		detection.Latitude, detection.Longitude, detection.Confidence,
		nullableFloat(detection.FRP), formatTime(detection.ObservedAt),
		boolInt(detection.Active), detection.RawJSON)
	if err != nil {
		return false, fmt.Errorf("insert detection: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("detection rows affected: %w", err)
	}
	return rows > 0, nil
}

func DeactivateDetectionTx(ctx context.Context, tx *sql.Tx, key string) error {
	_, err := tx.ExecContext(ctx, `UPDATE detections SET active = 0 WHERE detection_key = ?`, key)
	if err != nil {
		return fmt.Errorf("deactivate detection: %w", err)
	}
	return nil
}

func (s *Store) ActiveDetections(ctx context.Context) ([]domain.Detection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT detection_key, satellite, acq_date, acq_time, latitude, longitude,
		       confidence, frp, observed_at, raw_json
		FROM detections WHERE active = 1 ORDER BY detection_key`)
	if err != nil {
		return nil, fmt.Errorf("list active detections: %w", err)
	}
	defer rows.Close()

	var detections []domain.Detection
	for rows.Next() {
		var detection domain.Detection
		var observed string
		var frp sql.NullFloat64
		if err := rows.Scan(&detection.Key, &detection.Satellite, &detection.AcqDate, &detection.AcqTime,
			&detection.Latitude, &detection.Longitude, &detection.Confidence, &frp, &observed, &detection.RawJSON); err != nil {
			return nil, fmt.Errorf("scan active detection: %w", err)
		}
		if frp.Valid {
			value := frp.Float64
			detection.FRP = &value
		}
		detection.ObservedAt, _ = parseTime(observed)
		detection.Active = true
		detections = append(detections, detection)
	}
	return detections, rows.Err()
}

func (s *Store) ActiveFireIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT fire_id FROM fires WHERE active = 1 ORDER BY fire_id`)
	if err != nil {
		return nil, fmt.Errorf("list active fires: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan active fire: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) DetectionCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM detections`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count detections: %w", err)
	}
	return count, nil
}

func UpsertFireTx(ctx context.Context, tx *sql.Tx, fire domain.Fire) (int, error) {
	var version int
	err := tx.QueryRowContext(ctx, `
		INSERT INTO fires(
			fire_id, source_ref_id, event_json, version, first_detection_key,
			last_detection_at, detection_count, total_frp, active, updated_at
		) VALUES(?, ?, ?, 1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(fire_id) DO UPDATE SET
			event_json = excluded.event_json,
			version = fires.version + 1,
			first_detection_key = excluded.first_detection_key,
			last_detection_at = excluded.last_detection_at,
			detection_count = excluded.detection_count,
			total_frp = excluded.total_frp,
			active = excluded.active,
			updated_at = excluded.updated_at
		RETURNING version`,
		fire.FireID, fire.FireID, fire.EventJSON, fire.FirstDetectionKey,
		formatTime(fire.LastDetectionAt), fire.DetectionCount, fire.TotalFRP,
		boolInt(fire.Active), formatTime(time.Now())).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("upsert fire: %w", err)
	}
	return version, nil
}

func DeleteFireTx(ctx context.Context, tx *sql.Tx, fireID string) (int, error) {
	var version int
	err := tx.QueryRowContext(ctx, `
		UPDATE fires SET active = 0, version = version + 1, updated_at = ?
		WHERE fire_id = ? RETURNING version`, formatTime(time.Now()), fireID).Scan(&version)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("fire %s not found", fireID)
	}
	if err != nil {
		return 0, fmt.Errorf("delete fire: %w", err)
	}
	return version, nil
}

func (s *Store) Fire(ctx context.Context, fireID string) (*domain.Fire, error) {
	var fire domain.Fire
	var last, updated string
	var active int
	err := s.db.QueryRowContext(ctx, `
		SELECT fire_id, source_ref_id, event_json, version, first_detection_key,
		       last_detection_at, detection_count, total_frp, active, updated_at
		FROM fires WHERE fire_id = ?`, fireID).Scan(
		&fire.FireID, &fire.FireID, &fire.EventJSON, &fire.Version, &fire.FirstDetectionKey,
		&last, &fire.DetectionCount, &fire.TotalFRP, &active, &updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get fire: %w", err)
	}
	fire.LastDetectionAt, _ = parseTime(last)
	fire.Active = active != 0
	return &fire, nil
}

func EnqueueOutboxTx(ctx context.Context, tx *sql.Tx, item OutboxItem) (bool, error) {
	next := item.NextAttemptAt
	if next.IsZero() {
		next = time.Now().UTC()
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO outbox(producer_event_id, operation, source_position, payload, next_attempt_at)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(producer_event_id) DO NOTHING`,
		item.ProducerEventID, item.Operation, item.SourcePosition, item.Payload, formatTime(next))
	if err != nil {
		return false, fmt.Errorf("enqueue outbox: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("outbox rows affected: %w", err)
	}
	return rows > 0, nil
}

func (s *Store) PendingOutbox(ctx context.Context, limit int) ([]OutboxItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, producer_event_id, operation, source_position, payload, attempts, next_attempt_at, COALESCE(last_error, '')
		FROM outbox
		WHERE sent_at IS NULL AND parked_at IS NULL AND next_attempt_at <= ?
		ORDER BY id LIMIT ?`, formatTime(time.Now()), limit)
	if err != nil {
		return nil, fmt.Errorf("list outbox: %w", err)
	}
	defer rows.Close()

	items := []OutboxItem{}
	for rows.Next() {
		var item OutboxItem
		var next string
		if err := rows.Scan(&item.ID, &item.ProducerEventID, &item.Operation, &item.SourcePosition,
			&item.Payload, &item.Attempts, &next, &item.LastError); err != nil {
			return nil, fmt.Errorf("scan outbox: %w", err)
		}
		item.NextAttemptAt, _ = parseTime(next)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) MarkOutboxSent(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE outbox SET sent_at = ?, last_error = NULL WHERE id = ?`, formatTime(time.Now()), id)
	if err != nil {
		return fmt.Errorf("mark outbox sent: %w", err)
	}
	return nil
}

func (s *Store) MarkOutboxFailed(ctx context.Context, id int64, message string, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE outbox SET attempts = attempts + 1, last_error = ?, next_attempt_at = ? WHERE id = ?`,
		message, formatTime(next), id)
	if err != nil {
		return fmt.Errorf("mark outbox failed: %w", err)
	}
	return nil
}

func (s *Store) HasUnsentOutbox(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM outbox WHERE sent_at IS NULL AND parked_at IS NULL)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check unsent outbox: %w", err)
	}
	return exists, nil
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var stats Stats
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE sent_at IS NULL AND parked_at IS NULL`).Scan(&stats.OutboxPending); err != nil {
		return stats, fmt.Errorf("count outbox: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE parked_at IS NOT NULL`).Scan(&stats.OutboxParked); err != nil {
		return stats, fmt.Errorf("count parked outbox: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM detections`).Scan(&stats.Detections); err != nil {
		return stats, fmt.Errorf("count detections: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fires WHERE active = 1`).Scan(&stats.ActiveFires); err != nil {
		return stats, fmt.Errorf("count active fires: %w", err)
	}
	offset, err := s.Offset(ctx)
	if err != nil {
		return stats, err
	}
	stats.Offset = offset
	return stats, nil
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}
