package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// ProxyRepository is a PostgreSQL implementation of contract.ProxyRepository.
type ProxyRepository struct {
	driver contract.DatabaseDriver
}

func NewProxyRepository(driver contract.DatabaseDriver) contract.ProxyRepository {
	return &ProxyRepository{driver: driver}
}

const proxyColumns = `id, protocol, ip, port, country, anonymity, latency_ms, uptime_percent,
	status, last_scanned_at, last_alive_at, first_seen_at, source, fail_count, locked_by, locked_at, created_at, updated_at`

func scanProxy(row rowScanner) (*contract.ProxyRecord, error) {
	var p contract.ProxyRecord
	var country, anonymity, lockedBy sql.NullString
	var latencyMS sql.NullInt64
	var uptimePercent sql.NullFloat64
	var lastScannedAt, lastAliveAt, lockedAt sql.NullTime

	err := row.Scan(
		&p.ID, &p.Protocol, &p.IP, &p.Port,
		&country, &anonymity, &latencyMS, &uptimePercent,
		&p.Status, &lastScannedAt, &lastAliveAt, &p.FirstSeenAt,
		&p.Source, &p.FailCount, &lockedBy, &lockedAt,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	p.Country = sqlStringPtr(country)
	p.Anonymity = sqlStringPtr(anonymity)
	p.LatencyMS = sqlIntPtr(latencyMS)
	p.UptimePercent = sqlFloat64Ptr(uptimePercent)
	p.LastScannedAt = sqlTimePtr(lastScannedAt)
	p.LastAliveAt = sqlTimePtr(lastAliveAt)
	p.LockedBy = sqlStringPtr(lockedBy)
	p.LockedAt = sqlTimePtr(lockedAt)
	return &p, nil
}

func (r *ProxyRepository) Upsert(ctx context.Context, rec contract.ProxyRecord) (int64, bool, error) {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return 0, false, err
	}

	// Check if the proxy already exists.
	var existingID int64
	err = db.QueryRowContext(
		ctx,
		`SELECT id FROM proxies WHERE protocol = $1 AND ip = $2 AND port = $3`,
		rec.Protocol, rec.IP, rec.Port,
	).Scan(&existingID)

	if err == nil {
		// Already exists — update metadata.
		_, err = db.ExecContext(
			ctx,
			`UPDATE proxies SET
				country = COALESCE($1, country),
				anonymity = COALESCE($2, anonymity),
				uptime_percent = COALESCE($3, uptime_percent),
				source = $4,
				updated_at = $5
			WHERE id = $6`,
			nullableString(rec.Country), nullableString(rec.Anonymity),
			nullableFloat64(rec.UptimePercent), rec.Source,
			time.Now().UTC(), existingID,
		)
		if err != nil {
			return existingID, true, fmt.Errorf("proxy upsert update: %w", err)
		}
		return existingID, true, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, fmt.Errorf("proxy upsert check: %w", err)
	}

	// Insert new proxy.
	now := time.Now().UTC()
	var newID int64
	err = db.QueryRowContext(
		ctx,
		`INSERT INTO proxies (protocol, ip, port, country, anonymity, latency_ms, uptime_percent,
			status, last_scanned_at, last_alive_at, first_seen_at, source, fail_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id`,
		rec.Protocol, rec.IP, rec.Port,
		nullableString(rec.Country), nullableString(rec.Anonymity),
		nullableInt(rec.LatencyMS), nullableFloat64(rec.UptimePercent),
		contract.ProxyStatusInactive, nil, nil,
		now, rec.Source, 0, now, now,
	).Scan(&newID)
	if err != nil {
		return 0, false, fmt.Errorf("proxy upsert insert: %w", err)
	}
	return newID, false, nil
}

func (r *ProxyRepository) Get(ctx context.Context, id int64) (*contract.ProxyRecord, error) {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	row := db.QueryRowContext(ctx,
		`SELECT `+proxyColumns+` FROM proxies WHERE id = $1`, id)
	p, err := scanProxy(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("proxy get: %w", err)
	}
	return p, nil
}

func (r *ProxyRepository) UpdateStatus(ctx context.Context, id int64, status string, latencyMS *int, failCount int, lastAliveAt *time.Time) error {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = db.ExecContext(
		ctx,
		`UPDATE proxies SET status = $1, latency_ms = $2, fail_count = $3,
			last_alive_at = $4, last_scanned_at = $5, updated_at = $6
		WHERE id = $7`,
		status, nullableInt(latencyMS), failCount,
		nullableTime(lastAliveAt), now, now, id,
	)
	if err != nil {
		return fmt.Errorf("proxy update status: %w", err)
	}
	return nil
}

func (r *ProxyRepository) UpdateScanTime(ctx context.Context, id int64) error {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = db.ExecContext(
		ctx,
		`UPDATE proxies SET last_scanned_at = $1, updated_at = $2 WHERE id = $3`,
		now, now, id,
	)
	if err != nil {
		return fmt.Errorf("proxy update scan time: %w", err)
	}
	return nil
}

func (r *ProxyRepository) ListForScan(ctx context.Context, limit int) ([]contract.ProxyRecord, error) {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT `+proxyColumns+` FROM proxies
		WHERE
			(status = 'inactive')
			OR (status = 'active' AND last_scanned_at < NOW() - INTERVAL '10 minutes')
			OR (status = 'dead' AND last_scanned_at < NOW() - INTERVAL '3 days')
		ORDER BY
			CASE
				WHEN status = 'inactive' THEN 0
				WHEN status = 'active' THEN 1
				WHEN status = 'dead' THEN 2
			END,
			last_scanned_at ASC NULLS FIRST
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("proxy list for scan: %w", err)
	}
	defer rows.Close()

	var proxies []contract.ProxyRecord
	for rows.Next() {
		p, err := scanProxy(rows)
		if err != nil {
			return nil, fmt.Errorf("proxy scan row: %w", err)
		}
		proxies = append(proxies, *p)
	}
	return proxies, rows.Err()
}

func (r *ProxyRepository) ListActive(ctx context.Context, limit int) ([]contract.ProxyRecord, error) {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT `+proxyColumns+` FROM proxies
		WHERE status = 'active'
		ORDER BY latency_ms ASC NULLS LAST
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("proxy list active: %w", err)
	}
	defer rows.Close()

	var proxies []contract.ProxyRecord
	for rows.Next() {
		p, err := scanProxy(rows)
		if err != nil {
			return nil, fmt.Errorf("proxy active row: %w", err)
		}
		proxies = append(proxies, *p)
	}
	return proxies, rows.Err()
}

func (r *ProxyRepository) Count(ctx context.Context) (int64, error) {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	var count int64
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM proxies`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("proxy count: %w", err)
	}
	return count, nil
}

func (r *ProxyRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT status, COUNT(*) FROM proxies GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("proxy count by status: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int64)
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("proxy count by status row: %w", err)
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

func (r *ProxyRepository) ClaimProxy(ctx context.Context, owner string, lockTTL time.Duration) (*contract.ProxyRecord, error) {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("proxy claim begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback is a no-op after commit

	now := time.Now().UTC()
	expireThreshold := now.Add(-lockTTL)

	// Prefer proxies confirmed alive within the last hour, ordered by uptime (highest)
	// then latency (lowest). Fall back to any active proxy if none are recently scanned.
	recentThreshold := now.Add(-1 * time.Hour)

	row := tx.QueryRowContext(ctx,
		`SELECT `+proxyColumns+` FROM proxies
		WHERE status = 'active'
			AND protocol IN ('http', 'https', 'socks4', 'socks5')
			AND (locked_at IS NULL OR locked_at < $1)
			AND last_alive_at >= $2
		ORDER BY uptime_percent DESC NULLS LAST, latency_ms ASC NULLS LAST
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, expireThreshold, recentThreshold)

	p, err := scanProxy(row)
	if err == sql.ErrNoRows {
		// Fallback: any active proxy (not recently scanned)
		row = tx.QueryRowContext(ctx,
			`SELECT `+proxyColumns+` FROM proxies
			WHERE status = 'active'
				AND protocol IN ('http', 'https', 'socks4', 'socks5')
				AND (locked_at IS NULL OR locked_at < $1)
			ORDER BY uptime_percent DESC NULLS LAST, latency_ms ASC NULLS LAST
			LIMIT 1
			FOR UPDATE SKIP LOCKED`, expireThreshold)
		p, err = scanProxy(row)
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("proxy claim fallback: %w", err)
		}
	}

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("proxy claim: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE proxies SET locked_by = $1, locked_at = $2, updated_at = $3 WHERE id = $4`,
		owner, now, now, p.ID)
	if err != nil {
		return nil, fmt.Errorf("proxy claim lock: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("proxy claim commit: %w", err)
	}

	p.LockedBy = &owner
	p.LockedAt = &now
	return p, nil
}

func (r *ProxyRepository) ExtendLock(ctx context.Context, id int64, owner string, lockTTL time.Duration) (bool, error) {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	expireThreshold := now.Add(-lockTTL)
	result, err := db.ExecContext(ctx,
		`UPDATE proxies SET locked_at = $1, updated_at = $2
		WHERE id = $3 AND locked_by = $4 AND locked_at >= $5`,
		now, now, id, owner, expireThreshold)
	if err != nil {
		return false, fmt.Errorf("proxy extend lock: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("proxy extend lock rows: %w", err)
	}
	return rows > 0, nil
}

func (r *ProxyRepository) ReleaseProxy(ctx context.Context, id int64, owner string) error {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = db.ExecContext(ctx,
		`UPDATE proxies SET locked_by = NULL, locked_at = NULL, updated_at = $1
		WHERE id = $2 AND locked_by = $3`,
		now, id, owner)
	if err != nil {
		return fmt.Errorf("proxy release: %w", err)
	}
	return nil
}

func (r *ProxyRepository) MarkFailedProxy(ctx context.Context, id int64, owner string) error {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = db.ExecContext(ctx,
		`UPDATE proxies SET locked_by = NULL, locked_at = NULL, status = 'inactive',
		fail_count = fail_count + 1, updated_at = $1
		WHERE id = $2 AND locked_by = $3`,
		now, id, owner)
	if err != nil {
		return fmt.Errorf("proxy mark failed: %w", err)
	}
	return nil
}
