// Package sqlobserver writes Lens telemetry events to a relational database table.
// It supports PostgreSQL, MySQL, and Microsoft SQL Server via dialect-aware DDL and DML.
// Events are buffered and flushed in batches to reduce write pressure.
package sqlobserver

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Vedanshu7/lens/internal/observability"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"
)

func init() {
	observability.Register("sql", func(cfg map[string]any) (observability.Observer, error) {
		driver, _ := cfg["driver"].(string)
		dsn, _ := cfg["dsn"].(string)
		table, _ := cfg["table"].(string)
		if table == "" {
			table = "lens_events"
		}
		batchSize := 100
		if v, ok := cfg["batchSize"].(int); ok && v > 0 {
			batchSize = v
		}
		flushMs := 1000
		if v, ok := cfg["flushIntervalMs"].(int); ok && v > 0 {
			flushMs = v
		}
		if driver == "" || dsn == "" {
			return nil, fmt.Errorf("sql observer: driver and dsn are required")
		}

		db, err := sql.Open(driver, dsn)
		if err != nil {
			return nil, fmt.Errorf("sql observer: open: %w", err)
		}
		if err := db.Ping(); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sql observer: ping: %w", err)
		}

		o := &sqlObserver{
			db:        db,
			driver:    driver,
			table:     table,
			batchSize: batchSize,
			flushInt:  time.Duration(flushMs) * time.Millisecond,
			buf:       make([]observability.Event, 0, batchSize),
			stopCh:    make(chan struct{}),
		}

		if err := o.ensureSchema(context.Background()); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sql observer: schema: %w", err)
		}

		go o.flusher()
		return o, nil
	})
}

// sqlObserver buffers events and flushes them to a SQL table in batches.
type sqlObserver struct {
	db        *sql.DB
	driver    string
	table     string
	batchSize int
	flushInt  time.Duration

	mu     sync.Mutex
	buf    []observability.Event
	stopCh chan struct{}
}

// Record buffers event e and triggers an immediate flush when the buffer reaches batchSize.
func (o *sqlObserver) Record(_ context.Context, e observability.Event) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	o.mu.Lock()
	o.buf = append(o.buf, e)
	flush := len(o.buf) >= o.batchSize
	o.mu.Unlock()
	if flush {
		go o.flush()
	}
	return nil
}

// Close stops the flush ticker, drains the remaining buffer, and closes the database connection.
func (o *sqlObserver) Close() error {
	close(o.stopCh)
	o.flush()
	return o.db.Close()
}

func (o *sqlObserver) flusher() {
	t := time.NewTicker(o.flushInt)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			o.flush()
		case <-o.stopCh:
			return
		}
	}
}

func (o *sqlObserver) flush() {
	o.mu.Lock()
	if len(o.buf) == 0 {
		o.mu.Unlock()
		return
	}
	batch := o.buf
	o.buf = make([]observability.Event, 0, o.batchSize)
	o.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		slog.Warn("sql observer: begin tx", "err", err)
		return
	}

	insert := o.insertSQL()
	for _, e := range batch {
		var pattern, key *string
		if e.Pattern != nil {
			p := *e.Pattern
			pattern = &p
		}
		if e.Key != nil {
			k := *e.Key
			key = &k
		}
		var errStr *string
		if e.Error != "" {
			s := e.Error
			errStr = &s
		}
		if _, err := tx.ExecContext(ctx, insert,
			e.Timestamp, e.Service, e.Instance,
			string(e.Kind), e.Transport,
			e.Success, errStr,
			e.LatencyMs,
			e.Confirmed, e.Total,
			pattern, key,
			e.PeerID,
		); err != nil {
			slog.Warn("sql observer: insert", "err", err)
		}
	}
	if err := tx.Commit(); err != nil {
		slog.Warn("sql observer: commit", "err", err)
		tx.Rollback() //nolint:errcheck
	}
}

// insertSQL returns the dialect-appropriate INSERT statement for the events table.
// PostgreSQL uses numbered placeholders ($1..$13); others use positional (?).
func (o *sqlObserver) insertSQL() string {
	cols := `(ts, service, instance, kind, transport, success, error,
		 latency_ms, confirmed, total, pattern, key, peer_id)`
	var stmt string
	switch o.driver {
	case "postgres":
		stmt = fmt.Sprintf(`INSERT INTO %s %s
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, o.table, cols)
	default:
		stmt = fmt.Sprintf(`INSERT INTO %s %s
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, o.table, cols)
	}
	return stmt
}

// ensureSchema creates the lens_events table and its indexes if they do not exist.
// DDL is dialect-specific for postgres, mysql, and sqlserver.
func (o *sqlObserver) ensureSchema(ctx context.Context) error {
	var create string
	var err error
	switch o.driver {
	case "postgres":
		stmts := []string{
			fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
				id          BIGSERIAL PRIMARY KEY,
				ts          TIMESTAMPTZ      NOT NULL,
				service     VARCHAR(128)     NOT NULL,
				instance    VARCHAR(128)     NOT NULL,
				kind        VARCHAR(32)      NOT NULL,
				transport   VARCHAR(32),
				success     BOOLEAN          NOT NULL DEFAULT false,
				error       TEXT,
				latency_ms  DOUBLE PRECISION,
				confirmed   INT,
				total       INT,
				pattern     TEXT,
				key         TEXT,
				peer_id     VARCHAR(128),
				meta        JSONB
			)`, o.table),
			fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_service_ts ON %s (service, ts)`, o.table, o.table),
			fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_kind_ts ON %s (kind, ts)`, o.table, o.table),
		}
		for _, stmt := range stmts {
			if _, execErr := o.db.ExecContext(ctx, stmt); execErr != nil {
				// 42P07 = duplicate_table, 42P16 = invalid_table_definition; both mean
				// another instance already ran this DDL concurrently — safe to ignore.
				if !isPGDuplicateErr(execErr) {
					return execErr
				}
			}
		}
		return nil
	case "mysql":
		create = "CREATE TABLE IF NOT EXISTS " + o.table + ` (
			id          BIGINT AUTO_INCREMENT PRIMARY KEY,
			ts          DATETIME(6)      NOT NULL,
			service     VARCHAR(128)     NOT NULL,
			instance    VARCHAR(128)     NOT NULL,
			kind        VARCHAR(32)      NOT NULL,
			transport   VARCHAR(32),
			success     TINYINT(1)       NOT NULL DEFAULT 0,
			error       TEXT,
			latency_ms  DOUBLE,
			confirmed   INT,
			total       INT,
			pattern     TEXT,
			` + "`key`" + `  TEXT,
			peer_id     VARCHAR(128),
			meta        JSON,
			INDEX idx_service_ts (service, ts),
			INDEX idx_kind_ts (kind, ts)
		)`
	case "sqlserver":
		create = fmt.Sprintf(`IF NOT EXISTS (SELECT * FROM sysobjects WHERE name='%s' AND xtype='U')
		CREATE TABLE %s (
			id          BIGINT IDENTITY(1,1) PRIMARY KEY,
			ts          DATETIME2        NOT NULL,
			service     NVARCHAR(128)    NOT NULL,
			instance    NVARCHAR(128)    NOT NULL,
			kind        NVARCHAR(32)     NOT NULL,
			transport   NVARCHAR(32),
			success     BIT              NOT NULL DEFAULT 0,
			error       NVARCHAR(MAX),
			latency_ms  FLOAT,
			confirmed   INT,
			total       INT,
			pattern     NVARCHAR(MAX),
			[key]       NVARCHAR(MAX),
			peer_id     NVARCHAR(128),
			meta        NVARCHAR(MAX)
		)`, o.table, o.table)
	default:
		return fmt.Errorf("unsupported driver: %s (use postgres|mysql|sqlserver)", o.driver)
	}
	_, err = o.db.ExecContext(ctx, create)
	return err
}

// isPGDuplicateErr returns true when err is a PostgreSQL "duplicate object"
// error (SQLSTATE 42P07 or 23505), which means concurrent DDL already succeeded.
func isPGDuplicateErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return contains(s, "42P07") || contains(s, "23505") || contains(s, "already exists")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && indexStr(s, sub) >= 0)
}

func indexStr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// QueryLatency returns per-minute latency percentiles for invalidation events
// of service over the given SQL interval string (e.g. "1 hour").
func (o *sqlObserver) QueryLatency(ctx context.Context, service, interval string) ([]observability.LatencyBucket, error) {
	var rows *sql.Rows
	var err error

	switch o.driver {
	case "postgres":
		rows, err = o.db.QueryContext(ctx, fmt.Sprintf(`
			SELECT date_trunc('minute', ts) AS bucket, transport,
				PERCENTILE_CONT(0.50) WITHIN GROUP (ORDER BY latency_ms) AS p50,
				PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY latency_ms) AS p95,
				PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY latency_ms) AS p99
			FROM %s
			WHERE kind = 'invalidate' AND service = $1 AND ts > NOW() - $2::interval
			GROUP BY bucket, transport ORDER BY bucket`, o.table), service, interval)
	case "mysql":
		rows, err = o.db.QueryContext(ctx, fmt.Sprintf(`
			SELECT DATE_FORMAT(ts, '%%Y-%%m-%%dT%%H:%%i:00Z') AS bucket, transport,
				AVG(latency_ms) AS p50, MAX(latency_ms) AS p95, MAX(latency_ms) AS p99
			FROM %s
			WHERE kind = 'invalidate' AND service = ? AND ts > DATE_SUB(NOW(), INTERVAL 1 HOUR)
			GROUP BY bucket, transport ORDER BY bucket`, o.table), service)
	default:
		return nil, fmt.Errorf("latency query not supported for driver %s", o.driver)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []observability.LatencyBucket
	for rows.Next() {
		var b observability.LatencyBucket
		var bucketStr string
		if err := rows.Scan(&bucketStr, &b.Transport, &b.P50, &b.P95, &b.P99); err != nil {
			continue
		}
		b.Bucket, _ = time.Parse(time.RFC3339, bucketStr)
		out = append(out, b)
	}
	return out, rows.Err()
}

// QueryDeadPods returns dead-pod detection events for service over interval.
func (o *sqlObserver) QueryDeadPods(ctx context.Context, service, interval string) ([]observability.DeadPodEvent, error) {
	var q string
	var args []any
	switch o.driver {
	case "postgres":
		q = fmt.Sprintf(`SELECT ts, peer_id, latency_ms FROM %s
			WHERE kind = 'dead_pod' AND service = $1 AND ts > NOW() - $2::interval
			ORDER BY ts DESC`, o.table)
		args = []any{service, interval}
	case "mysql":
		q = fmt.Sprintf(`SELECT ts, peer_id, latency_ms FROM %s
			WHERE kind = 'dead_pod' AND service = ? AND ts > DATE_SUB(NOW(), INTERVAL 24 HOUR)
			ORDER BY ts DESC`, o.table)
		args = []any{service}
	default:
		return nil, fmt.Errorf("dead pods query not supported for driver %s", o.driver)
	}

	rows, err := o.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []observability.DeadPodEvent
	for rows.Next() {
		var e observability.DeadPodEvent
		if err := rows.Scan(&e.Timestamp, &e.PeerID, &e.DetectionMs); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// QueryDiscovery returns peer discovery resolution events over interval.
func (o *sqlObserver) QueryDiscovery(ctx context.Context, interval string) ([]observability.DiscoveryEvent, error) {
	var q string
	var args []any
	switch o.driver {
	case "postgres":
		q = fmt.Sprintf(`SELECT ts, instance, confirmed, latency_ms FROM %s
			WHERE kind = 'discovery' AND ts > NOW() - $1::interval
			ORDER BY ts DESC LIMIT 200`, o.table)
		args = []any{interval}
	case "mysql":
		q = fmt.Sprintf(`SELECT ts, instance, confirmed, latency_ms FROM %s
			WHERE kind = 'discovery' AND ts > DATE_SUB(NOW(), INTERVAL 24 HOUR)
			ORDER BY ts DESC LIMIT 200`, o.table)
	default:
		return nil, fmt.Errorf("discovery query not supported for driver %s", o.driver)
	}

	rows, err := o.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []observability.DiscoveryEvent
	for rows.Next() {
		var e observability.DiscoveryEvent
		if err := rows.Scan(&e.Timestamp, &e.Instance, &e.PeerCount, &e.ResolutionMs); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// QueryFlow returns aggregated operation counts by kind and outcome for service over interval.
func (o *sqlObserver) QueryFlow(ctx context.Context, service, interval string) (*observability.FlowStats, error) {
	var q string
	var args []any
	switch o.driver {
	case "postgres":
		q = fmt.Sprintf(`SELECT kind, success,
			CASE WHEN kind='invalidate' AND confirmed < total THEN 'partial' ELSE '' END AS partial
			FROM %s WHERE service = $1 AND ts > NOW() - $2::interval`, o.table)
		args = []any{service, interval}
	case "mysql":
		q = fmt.Sprintf(`SELECT kind, success, '' AS partial FROM %s
			WHERE service = ? AND ts > DATE_SUB(NOW(), INTERVAL 24 HOUR)`, o.table)
		args = []any{service}
	default:
		return nil, fmt.Errorf("flow query not supported for driver %s", o.driver)
	}

	rows, err := o.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	stats := &observability.FlowStats{}
	for rows.Next() {
		var kind string
		var success bool
		var partial string
		if err := rows.Scan(&kind, &success, &partial); err != nil {
			continue
		}
		switch kind {
		case "invalidate":
			stats.Invalidate.Total++
			if partial == "partial" {
				stats.Invalidate.Partial++
			} else if success {
				stats.Invalidate.Success++
			} else {
				stats.Invalidate.Failure++
			}
		case "fetch":
			stats.Fetch.Total++
			if success {
				stats.Fetch.Success++
			} else {
				stats.Fetch.Failure++
			}
		case "replay":
			stats.Replay.Total++
		}
	}
	return stats, rows.Err()
}

// QuerySummary returns aggregate metrics for service over interval.
// Only PostgreSQL is supported; other drivers return an error.
func (o *sqlObserver) QuerySummary(ctx context.Context, service, interval string) (*observability.SummaryStats, error) {
	var q string
	var args []any
	switch o.driver {
	case "postgres":
		q = fmt.Sprintf(`SELECT
			COUNT(*) FILTER (WHERE kind='invalidate'),
			COALESCE(AVG(latency_ms) FILTER (WHERE kind='invalidate'), 0),
			COALESCE(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE kind='invalidate'), 0),
			COALESCE(COUNT(*) FILTER (WHERE kind='invalidate' AND NOT success)::float / NULLIF(COUNT(*) FILTER (WHERE kind='invalidate'), 0) * 100, 0),
			COUNT(*) FILTER (WHERE kind='dead_pod'),
			COUNT(*) FILTER (WHERE kind='peer_join'),
			COUNT(*) FILTER (WHERE kind='peer_leave')
		FROM %s WHERE service = $1 AND ts > NOW() - $2::interval`, o.table)
		args = []any{service, interval}
	default:
		return nil, fmt.Errorf("summary query not supported for driver %s", o.driver)
	}

	row := o.db.QueryRowContext(ctx, q, args...)
	stats := &observability.SummaryStats{}
	err := row.Scan(
		&stats.TotalInvalidations,
		&stats.AvgLatencyMs,
		&stats.P99LatencyMs,
		&stats.FailureRatePct,
		&stats.DeadPodsDetected,
		&stats.PeersJoined,
		&stats.PeersLeft,
	)
	return stats, err
}

// Compile-time assertion that sqlObserver satisfies the SQLQuerier interface.
var _ observability.SQLQuerier = (*sqlObserver)(nil)
