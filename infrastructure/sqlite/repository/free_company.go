package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

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

func freeCompanyFilterWhere(f contract.FreeCompanyFilter) (string, []any) {
	var conds []string
	var args []any
	if f.World != "" {
		conds = append(conds, "world = ?")
		args = append(args, f.World)
	}
	if f.Datacenter != "" {
		conds = append(conds, "datacenter = ?")
		args = append(args, f.Datacenter)
	}
	if f.Name != "" {
		conds = append(conds, "name LIKE ?")
		args = append(args, "%"+f.Name+"%")
	}
	if f.GrandCompany != "" {
		conds = append(conds, "grand_company = ?")
		args = append(args, f.GrandCompany)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func freeCompanyOrderBy(sortBy, sortOrder string) string {
	order := "ASC"
	if strings.EqualFold(sortOrder, "desc") {
		order = "DESC"
	}

	switch strings.ToLower(sortBy) {
	case "name":
		return " ORDER BY LOWER(name) " + order + ", id ASC"
	case "world":
		return " ORDER BY world " + order + ", id ASC"
	case "member_count", "members":
		return " ORDER BY member_count " + order + ", id ASC"
	case "formed", "formed_at":
		return " ORDER BY formed_at " + order + ", id ASC"
	default:
		return " ORDER BY member_count DESC, id ASC"
	}
}

func (r *FreeCompanyRepository) List(ctx context.Context, f contract.FreeCompanyFilter, limit, offset int) ([]contract.FreeCompanyRecord, error) {
	where, args := freeCompanyFilterWhere(f)
	orderBy := freeCompanyOrderBy(f.SortBy, f.SortOrder)
	q := `SELECT id, name, world, datacenter, member_count, formed_at, last_seen_at
		   FROM free_companies` + where + orderBy + ` LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := r.driver.FetchMany(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.FreeCompanyRecord
	for rows.Next() {
		var rec contract.FreeCompanyRecord
		var formedAt sql.NullString
		var lastSeen string
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.World, &rec.Datacenter, &rec.MemberCount,
			&formedAt, &lastSeen); err != nil {
			return nil, err
		}
		rec.FormedAt = sqlTimePtr(formedAt)
		if t, err := parseTime(lastSeen); err == nil {
			rec.LastSeenAt = t
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (r *FreeCompanyRepository) Count(ctx context.Context, f contract.FreeCompanyFilter) (int64, error) {
	where, args := freeCompanyFilterWhere(f)
	q := `SELECT COUNT(*) FROM free_companies` + where
	row, err := r.driver.FetchOne(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
