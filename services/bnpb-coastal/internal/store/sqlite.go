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

	"github.com/angkaberapa/AAT/services/bnpb-coastal/internal/domain"
)

const schema = `
CREATE TABLE IF NOT EXISTS state (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS observations (
	observation_id TEXT PRIMARY KEY,
	station_id TEXT NOT NULL,
	observed_at TEXT NOT NULL,
	received_at TEXT NOT NULL,
	critical INTEGER NOT NULL,
	quality TEXT NOT NULL,
	sea_level_anomaly_m REAL,
	wave_height_m REAL,
	raw_json TEXT NOT NULL
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
	sent_at TEXT
);
CREATE INDEX IF NOT EXISTS outbox_pending ON outbox(sent_at, next_attempt_at, id);

CREATE TABLE IF NOT EXISTS quarantine (
	message_id TEXT PRIMARY KEY,
	source_position TEXT NOT NULL,
	received_at TEXT NOT NULL,
	reason TEXT NOT NULL,
	retryable INTEGER NOT NULL,
	status TEXT NOT NULL,
	raw_payload_base64 TEXT NOT NULL,
	content_type TEXT NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	next_attempt_at TEXT NOT NULL,
	last_error TEXT,
	sent_at TEXT
);
CREATE INDEX IF NOT EXISTS quarantine_pending ON quarantine(sent_at, next_attempt_at, message_id);

CREATE TABLE IF NOT EXISTS cache (
	station_id TEXT PRIMARY KEY,
	observation_id TEXT NOT NULL,
	observed_at TEXT NOT NULL,
	received_at TEXT NOT NULL,
	payload TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
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

type QuarantineItem struct {
	MessageID        string
	SourcePosition   string
	ReceivedAt       time.Time
	Reason           string
	Retryable        bool
	Status           string
	RawPayloadBase64 string
	ContentType      string
	Attempts         int
	NextAttemptAt    time.Time
	LastError        string
}

type Stats struct {
	OutboxPending     int    `json:"outbox_pending"`
	QuarantinePending int    `json:"quarantine_pending"`
	Observations      int    `json:"observations"`
	CacheEntries      int    `json:"cache_entries"`
	Cursor            string `json:"cursor"`
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
	// SQLite's URI parser on Windows rejects file:///C:/... (it treats C: as an
	// authority). file:C:/... is the form modernc accepts.
	dbPath := strings.ReplaceAll(filepath.ToSlash(absolute), " ", "%20")
	dsn := "file:" + dbPath + "?" + query.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite has one writer. Serializing access avoids SQLITE_BUSY and keeps
	// cursor/outbox commits deterministic.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

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

func (s *Store) Cursor(ctx context.Context) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM state WHERE key = 'coastal_cursor'`).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read cursor: %w", err)
	}
	return value, nil
}

func SetCursorTx(ctx context.Context, tx *sql.Tx, cursor string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO state(key, value) VALUES('coastal_cursor', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, cursor)
	if err != nil {
		return fmt.Errorf("save cursor: %w", err)
	}
	return nil
}

func InsertObservationTx(ctx context.Context, tx *sql.Tx, observation domain.Observation) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO observations(
			observation_id, station_id, observed_at, received_at, critical, quality,
			sea_level_anomaly_m, wave_height_m, raw_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(observation_id) DO NOTHING`,
		observation.ObservationID,
		observation.StationID,
		formatTime(observation.ObservedAt),
		formatTime(observation.ReceivedAt),
		boolInt(observation.Critical),
		observation.Quality,
		nullableFloat(observation.SeaLevelAnomalyM),
		nullableFloat(observation.WaveHeightM),
		observation.RawJSON,
	)
	if err != nil {
		return false, fmt.Errorf("insert observation: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("observation rows affected: %w", err)
	}
	return rows > 0, nil
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

func EnqueueQuarantineTx(ctx context.Context, tx *sql.Tx, item QuarantineItem) (bool, error) {
	next := item.NextAttemptAt
	if next.IsZero() {
		next = time.Now().UTC()
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO quarantine(
			message_id, source_position, received_at, reason, retryable, status,
			raw_payload_base64, content_type, next_attempt_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id) DO NOTHING`,
		item.MessageID, item.SourcePosition, formatTime(item.ReceivedAt), item.Reason,
		boolInt(item.Retryable), item.Status, item.RawPayloadBase64, item.ContentType, formatTime(next))
	if err != nil {
		return false, fmt.Errorf("enqueue quarantine: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("quarantine rows affected: %w", err)
	}
	return rows > 0, nil
}

func UpdateCacheTx(ctx context.Context, tx *sql.Tx, observation domain.Observation) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO cache(station_id, observation_id, observed_at, received_at, payload, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(station_id) DO UPDATE SET
			observation_id = excluded.observation_id,
			observed_at = excluded.observed_at,
			received_at = excluded.received_at,
			payload = excluded.payload,
			updated_at = excluded.updated_at`,
		observation.StationID, observation.ObservationID,
		formatTime(observation.ObservedAt), formatTime(observation.ReceivedAt),
		observation.RawJSON, formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("update cache: %w", err)
	}
	return nil
}

func (s *Store) PendingOutbox(ctx context.Context, limit int) ([]OutboxItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, producer_event_id, operation, source_position, payload, attempts, next_attempt_at, COALESCE(last_error, '')
		FROM outbox
		WHERE sent_at IS NULL AND next_attempt_at <= ?
		ORDER BY id
		LIMIT ?`, formatTime(time.Now()), limit)
	if err != nil {
		return nil, fmt.Errorf("list outbox: %w", err)
	}
	defer rows.Close()

	items := []OutboxItem{}
	for rows.Next() {
		var item OutboxItem
		var next string
		if err := rows.Scan(&item.ID, &item.ProducerEventID, &item.Operation, &item.SourcePosition, &item.Payload, &item.Attempts, &next, &item.LastError); err != nil {
			return nil, fmt.Errorf("scan outbox: %w", err)
		}
		item.NextAttemptAt, _ = parseTime(next)
		items = append(items, item)
	}
	return items, rows.Err()
}

// HasUnsentOutbox reports whether any outbox row is still unsent, including
// rows whose next_attempt_at is in the future. Cursor commits must use this,
// not PendingOutbox, or a failed item would be skipped and the cursor would
// move past data that was never accepted by the Aggregator.
func (s *Store) HasUnsentOutbox(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM outbox WHERE sent_at IS NULL)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check unsent outbox: %w", err)
	}
	return exists, nil
}

func (s *Store) MarkOutboxSent(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE outbox SET sent_at = ?, last_error = NULL WHERE id = ?`, formatTime(time.Now()), id)
	if err != nil {
		return fmt.Errorf("mark outbox sent: %w", err)
	}
	return nil
}

func (s *Store) MarkOutboxFailed(ctx context.Context, id int64, message string, nextAttempt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE outbox SET attempts = attempts + 1, last_error = ?, next_attempt_at = ? WHERE id = ?`,
		message, formatTime(nextAttempt), id)
	if err != nil {
		return fmt.Errorf("mark outbox failed: %w", err)
	}
	return nil
}

func (s *Store) PendingQuarantine(ctx context.Context, limit int) ([]QuarantineItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, source_position, received_at, reason, retryable, status,
		       raw_payload_base64, content_type, attempts, next_attempt_at, COALESCE(last_error, '')
		FROM quarantine
		WHERE sent_at IS NULL AND next_attempt_at <= ?
		ORDER BY message_id
		LIMIT ?`, formatTime(time.Now()), limit)
	if err != nil {
		return nil, fmt.Errorf("list quarantine: %w", err)
	}
	defer rows.Close()

	items := []QuarantineItem{}
	for rows.Next() {
		var item QuarantineItem
		var receivedAt, next string
		var retryable int
		if err := rows.Scan(&item.MessageID, &item.SourcePosition, &receivedAt, &item.Reason, &retryable, &item.Status,
			&item.RawPayloadBase64, &item.ContentType, &item.Attempts, &next, &item.LastError); err != nil {
			return nil, fmt.Errorf("scan quarantine: %w", err)
		}
		item.ReceivedAt, _ = parseTime(receivedAt)
		item.NextAttemptAt, _ = parseTime(next)
		item.Retryable = retryable != 0
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) HasUnsentQuarantine(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM quarantine WHERE sent_at IS NULL)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check unsent quarantine: %w", err)
	}
	return exists, nil
}

func (s *Store) MarkQuarantineSent(ctx context.Context, messageID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE quarantine SET sent_at = ?, last_error = NULL WHERE message_id = ?`, formatTime(time.Now()), messageID)
	if err != nil {
		return fmt.Errorf("mark quarantine sent: %w", err)
	}
	return nil
}

func (s *Store) MarkQuarantineFailed(ctx context.Context, messageID, message string, nextAttempt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE quarantine SET attempts = attempts + 1, last_error = ?, next_attempt_at = ? WHERE message_id = ?`,
		message, formatTime(nextAttempt), messageID)
	if err != nil {
		return fmt.Errorf("mark quarantine failed: %w", err)
	}
	return nil
}

func (s *Store) LastEventAt(ctx context.Context) (*time.Time, error) {
	var value sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT MAX(observed_at) FROM observations`).Scan(&value)
	if err != nil {
		return nil, fmt.Errorf("last event: %w", err)
	}
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, fmt.Errorf("parse last event: %w", err)
	}
	return &parsed, nil
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var stats Stats
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE sent_at IS NULL`).Scan(&stats.OutboxPending); err != nil {
		return stats, fmt.Errorf("count outbox: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM quarantine WHERE sent_at IS NULL`).Scan(&stats.QuarantinePending); err != nil {
		return stats, fmt.Errorf("count quarantine: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations`).Scan(&stats.Observations); err != nil {
		return stats, fmt.Errorf("count observations: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cache`).Scan(&stats.CacheEntries); err != nil {
		return stats, fmt.Errorf("count cache: %w", err)
	}
	cursor, err := s.Cursor(ctx)
	if err != nil {
		return stats, err
	}
	stats.Cursor = cursor
	return stats, nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

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
