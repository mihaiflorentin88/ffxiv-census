package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// FreeCompanyRepository is a SQLite implementation of contract.FreeCompanyRepository.
type FreeCompanyRepository struct {
	driver contract.SQLiteDriver
}

func NewFreeCompanyRepository(driver contract.SQLiteDriver) contract.FreeCompanyRepository {
	return &FreeCompanyRepository{driver: driver}
}

func (r *FreeCompanyRepository) Upsert(ctx context.Context, rec contract.FreeCompanyRecord) error {
	_, err := r.driver.Execute(ctx,
		`INSERT INTO free_companies (id, name, world, datacenter, member_count, formed_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			world = excluded.world,
			datacenter = excluded.datacenter,
			member_count = excluded.member_count,
			formed_at = excluded.formed_at,
			last_seen_at = excluded.last_seen_at`,
		rec.ID, rec.Name, rec.World, rec.Datacenter, rec.MemberCount,
		nullableTime(rec.FormedAt), formatTime(rec.LastSeenAt))
	if err != nil {
		return fmt.Errorf("free company upsert: %w", err)
	}
	return nil
}

func (r *FreeCompanyRepository) Get(ctx context.Context, id string) (*contract.FreeCompanyRecord, error) {
	row, err := r.driver.FetchOne(ctx,
		`SELECT id, name, world, datacenter, member_count, formed_at, last_seen_at
		   FROM free_companies WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	var rec contract.FreeCompanyRecord
	var formedAt sql.NullString
	var lastSeen string
	if err := row.Scan(&rec.ID, &rec.Name, &rec.World, &rec.Datacenter, &rec.MemberCount,
		&formedAt, &lastSeen); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	rec.FormedAt = sqlTimePtr(formedAt)
	if t, err := parseTime(lastSeen); err == nil {
		rec.LastSeenAt = t
	}
	return &rec, nil
}
