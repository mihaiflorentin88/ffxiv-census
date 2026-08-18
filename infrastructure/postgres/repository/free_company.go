package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// FreeCompanyRepository is a PostgreSQL implementation of contract.FreeCompanyRepository.
type FreeCompanyRepository struct {
	driver contract.DatabaseDriver
}

func NewFreeCompanyRepository(driver contract.DatabaseDriver) contract.FreeCompanyRepository {
	return &FreeCompanyRepository{driver: driver}
}

func (r *FreeCompanyRepository) Upsert(ctx context.Context, rec contract.FreeCompanyRecord) error {
	_, err := r.driver.Execute(ctx,
		`INSERT INTO free_companies (id, name, world, datacenter, member_count, formed_at, last_seen_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (id) DO UPDATE SET
			name = excluded.name,
			world = excluded.world,
			datacenter = excluded.datacenter,
			member_count = excluded.member_count,
			formed_at = COALESCE(excluded.formed_at, free_companies.formed_at),
			last_seen_at = excluded.last_seen_at`,
		rec.ID, rec.Name, rec.World, rec.Datacenter, rec.MemberCount,
		nullableTime(rec.FormedAt), rec.LastSeenAt)
	if err != nil {
		return fmt.Errorf("free company upsert: %w", err)
	}
	return nil
}

func (r *FreeCompanyRepository) Get(ctx context.Context, id string) (*contract.FreeCompanyRecord, error) {
	row, err := r.driver.FetchOne(ctx,
		`SELECT id, name, world, datacenter, member_count, formed_at, last_seen_at
		   FROM free_companies WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	var rec contract.FreeCompanyRecord
	var formedAt sql.NullTime
	var lastSeen time.Time
	if err := row.Scan(&rec.ID, &rec.Name, &rec.World, &rec.Datacenter, &rec.MemberCount,
		&formedAt, &lastSeen); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("free company scan: %w", err)
	}
	rec.FormedAt = sqlTimePtr(formedAt)
	rec.LastSeenAt = lastSeen
	return &rec, nil
}

func freeCompanyFilterWhere(f contract.FreeCompanyFilter) (string, []any) {
	var where []string
	var args []any

	addParam := func(clauseTpl string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(clauseTpl, len(args)))
	}

	if f.World != "" {
		addParam("world = $%d", f.World)
	}
	if f.Datacenter != "" {
		addParam("datacenter = $%d", f.Datacenter)
	}
	if f.Name != "" {
		addParam("name ILIKE $%d", "%"+f.Name+"%")
	}
	if len(where) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(where, " AND "), args
}

func freeCompanyOrderBy(sortBy, sortOrder string) string {
	var col string
	switch strings.ToLower(sortBy) {
	case "name":
		col = "name"
	case "world":
		col = "world"
	case "members", "member_count":
		col = "member_count"
	case "formed_at":
		col = "formed_at"
	default:
		col = "last_seen_at"
	}

	var dir string
	if strings.ToLower(sortOrder) == "desc" {
		dir = "DESC"
	} else {
		dir = "ASC"
	}
	return fmt.Sprintf("%s %s", col, dir)
}

func (r *FreeCompanyRepository) List(ctx context.Context, f contract.FreeCompanyFilter, limit, offset int) ([]contract.FreeCompanyRecord, error) {
	where, args := freeCompanyFilterWhere(f)
	orderBy := freeCompanyOrderBy(f.SortBy, f.SortOrder)

	args = append(args, limit, offset)
	limitPos := len(args) - 1
	offsetPos := len(args)

	query := fmt.Sprintf(`SELECT id, name, world, datacenter, member_count, formed_at, last_seen_at
	                        FROM free_companies
	                       %s
	                       ORDER BY %s
	                       LIMIT $%d OFFSET $%d`, where, orderBy, limitPos, offsetPos)

	rows, err := r.driver.FetchMany(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contract.FreeCompanyRecord
	for rows.Next() {
		var rec contract.FreeCompanyRecord
		var formedAt sql.NullTime
		var lastSeen time.Time
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.World, &rec.Datacenter, &rec.MemberCount,
			&formedAt, &lastSeen); err != nil {
			return nil, fmt.Errorf("free company list scan: %w", err)
		}
		rec.FormedAt = sqlTimePtr(formedAt)
		rec.LastSeenAt = lastSeen
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (r *FreeCompanyRepository) Count(ctx context.Context, f contract.FreeCompanyFilter) (int64, error) {
	where, args := freeCompanyFilterWhere(f)
	query := fmt.Sprintf(`SELECT COUNT(*) FROM free_companies %s`, where)
	row, err := r.driver.FetchOne(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	var count int64
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
