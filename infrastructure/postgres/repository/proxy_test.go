package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestProxyRepository_ListForScan_ExcludesDead(t *testing.T) {
	driver := newTestDriver(t)
	repo := repository.NewProxyRepository(driver)
	ctx := context.Background()

	now := time.Now().UTC()

	// Seed two inactive rows scanned 21 and 22 minutes ago.
	for i, minutesAgo := range []int{21, 22} {
		scannedAt := now.Add(-time.Duration(minutesAgo) * time.Minute)
		_, err := driver.Execute(ctx, `
			INSERT INTO proxies (protocol, ip, port, status, last_scanned_at, source, fail_count, created_at, updated_at, first_seen_at)
			VALUES ($1, $2, $3, 'inactive', $4, 'test', 0, $5, $5, $5)`,
			"http", "inactive"+string(rune('a'+i))+".example.com", 8080, scannedAt, now)
		if err != nil {
			t.Fatalf("seed inactive %d: %v", i, err)
		}
	}

	// Seed two active rows scanned 11 and 12 minutes ago.
	for i, minutesAgo := range []int{11, 12} {
		scannedAt := now.Add(-time.Duration(minutesAgo) * time.Minute)
		_, err := driver.Execute(ctx, `
			INSERT INTO proxies (protocol, ip, port, status, last_scanned_at, source, fail_count, created_at, updated_at, first_seen_at)
			VALUES ($1, $2, $3, 'active', $4, 'test', 0, $5, $5, $5)`,
			"http", "active"+string(rune('a'+i))+".example.com", 8081, scannedAt, now)
		if err != nil {
			t.Fatalf("seed active %d: %v", i, err)
		}
	}

	// Seed two dead rows scanned 8 and 9 days ago.
	for i, daysAgo := range []int{8, 9} {
		scannedAt := now.Add(-time.Duration(daysAgo) * 24 * time.Hour)
		_, err := driver.Execute(ctx, `
			INSERT INTO proxies (protocol, ip, port, status, last_scanned_at, source, fail_count, created_at, updated_at, first_seen_at)
			VALUES ($1, $2, $3, 'dead', $4, 'test', 5, $5, $5, $5)`,
			"http", "dead"+string(rune('a'+i))+".example.com", 8082, scannedAt, now)
		if err != nil {
			t.Fatalf("seed dead %d: %v", i, err)
		}
	}

	// ListForScan should return only the 4 active/inactive rows, not dead.
	all, err := repo.ListForScan(ctx, 100)
	if err != nil {
		t.Fatalf("ListForScan: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("ListForScan returned %d rows, want 4 (active+inactive only)", len(all))
	}
	for _, p := range all {
		if p.Status == contract.ProxyStatusDead {
			t.Errorf("ListForScan returned dead proxy ID=%d — dead must be excluded", p.ID)
		}
	}

	// Verify ordering: inactive before active, oldest scan first within each group.
	if all[0].Status != contract.ProxyStatusInactive || all[1].Status != contract.ProxyStatusInactive {
		t.Errorf("first two should be inactive, got %s, %s", all[0].Status, all[1].Status)
	}
	if all[2].Status != contract.ProxyStatusActive || all[3].Status != contract.ProxyStatusActive {
		t.Errorf("last two should be active, got %s, %s", all[2].Status, all[3].Status)
	}
	// Oldest scan first within inactive: 22 min ago before 21 min ago.
	if all[0].LastScannedAt.After(*all[1].LastScannedAt) {
		t.Error("inactive: oldest scan should come first")
	}
	// Oldest scan first within active: 12 min ago before 11 min ago.
	if all[2].LastScannedAt.After(*all[3].LastScannedAt) {
		t.Error("active: oldest scan should come first")
	}

	// limit=1 should return the oldest inactive.
	one, err := repo.ListForScan(ctx, 1)
	if err != nil {
		t.Fatalf("ListForScan limit=1: %v", err)
	}
	if len(one) != 1 {
		t.Fatalf("ListForScan limit=1 returned %d rows, want 1", len(one))
	}
	if one[0].Status != contract.ProxyStatusInactive {
		t.Errorf("limit=1 should return inactive, got %s", one[0].Status)
	}
}

func TestProxyRepository_ListDeadForScan_OnlyDead(t *testing.T) {
	driver := newTestDriver(t)
	repo := repository.NewProxyRepository(driver)
	ctx := context.Background()

	now := time.Now().UTC()

	// Seed two inactive rows scanned 21 and 22 minutes ago.
	for i, minutesAgo := range []int{21, 22} {
		scannedAt := now.Add(-time.Duration(minutesAgo) * time.Minute)
		_, err := driver.Execute(ctx, `
			INSERT INTO proxies (protocol, ip, port, status, last_scanned_at, source, fail_count, created_at, updated_at, first_seen_at)
			VALUES ($1, $2, $3, 'inactive', $4, 'test', 0, $5, $5, $5)`,
			"http", "inactive"+string(rune('a'+i))+".example.com", 8080, scannedAt, now)
		if err != nil {
			t.Fatalf("seed inactive %d: %v", i, err)
		}
	}

	// Seed two active rows scanned 11 and 12 minutes ago.
	for i, minutesAgo := range []int{11, 12} {
		scannedAt := now.Add(-time.Duration(minutesAgo) * time.Minute)
		_, err := driver.Execute(ctx, `
			INSERT INTO proxies (protocol, ip, port, status, last_scanned_at, source, fail_count, created_at, updated_at, first_seen_at)
			VALUES ($1, $2, $3, 'active', $4, 'test', 0, $5, $5, $5)`,
			"http", "active"+string(rune('a'+i))+".example.com", 8081, scannedAt, now)
		if err != nil {
			t.Fatalf("seed active %d: %v", i, err)
		}
	}

	// Seed two dead rows scanned 8 and 9 days ago.
	for i, daysAgo := range []int{8, 9} {
		scannedAt := now.Add(-time.Duration(daysAgo) * 24 * time.Hour)
		_, err := driver.Execute(ctx, `
			INSERT INTO proxies (protocol, ip, port, status, last_scanned_at, source, fail_count, created_at, updated_at, first_seen_at)
			VALUES ($1, $2, $3, 'dead', $4, 'test', 5, $5, $5, $5)`,
			"http", "dead"+string(rune('a'+i))+".example.com", 8082, scannedAt, now)
		if err != nil {
			t.Fatalf("seed dead %d: %v", i, err)
		}
	}

	// ListDeadForScan should return only the 2 dead rows.
	dead, err := repo.ListDeadForScan(ctx, 100)
	if err != nil {
		t.Fatalf("ListDeadForScan: %v", err)
	}
	if len(dead) != 2 {
		t.Fatalf("ListDeadForScan returned %d rows, want 2 (dead only)", len(dead))
	}
	for _, p := range dead {
		if p.Status != contract.ProxyStatusDead {
			t.Errorf("ListDeadForScan returned non-dead proxy ID=%d status=%s", p.ID, p.Status)
		}
	}

	// Oldest scan first: 9 days ago before 8 days ago.
	if dead[0].LastScannedAt.After(*dead[1].LastScannedAt) {
		t.Error("dead: oldest scan should come first")
	}

	// limit=1 should return the oldest dead.
	one, err := repo.ListDeadForScan(ctx, 1)
	if err != nil {
		t.Fatalf("ListDeadForScan limit=1: %v", err)
	}
	if len(one) != 1 {
		t.Fatalf("ListDeadForScan limit=1 returned %d rows, want 1", len(one))
	}
	if one[0].Status != contract.ProxyStatusDead {
		t.Errorf("limit=1 should return dead, got %s", one[0].Status)
	}
}
