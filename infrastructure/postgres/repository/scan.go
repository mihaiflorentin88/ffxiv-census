package repository

import (
	"database/sql"
	"time"
)

type rowScanner interface {
	Scan(dest ...any) error
}

func nullableString(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func nullableUint32(v *uint32) any {
	if v == nil {
		return nil
	}
	return int64(*v)
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func sqlStringPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

func sqlUint32Ptr(ni sql.NullInt64) *uint32 {
	if !ni.Valid {
		return nil
	}
	v := uint32(ni.Int64)
	return &v
}

func sqlTimePtr(nt sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}
	t := nt.Time
	return &t
}
