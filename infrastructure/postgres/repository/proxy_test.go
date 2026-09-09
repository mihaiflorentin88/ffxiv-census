package repository_test

import (
	"context"
	"fmt"
	"sync"
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

	// The 100-row call above claimed every eligible row (stamped at claim
	// time), so nothing is eligible anymore.
	one, err := repo.ListForScan(ctx, 1)
	if err != nil {
		t.Fatalf("ListForScan limit=1: %v", err)
	}
	if len(one) != 0 {
		t.Fatalf("ListForScan limit=1 returned %d rows, want 0 (all rows already claimed)", len(one))
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

	// The 100-row call above claimed both eligible dead rows, so nothing is
	// eligible anymore.
	one, err := repo.ListDeadForScan(ctx, 1)
	if err != nil {
		t.Fatalf("ListDeadForScan limit=1: %v", err)
	}
	if len(one) != 0 {
		t.Fatalf("ListDeadForScan limit=1 returned %d rows, want 0 (all rows already claimed)", len(one))
	}
}

func TestProxyRepository_ListForScan_ClaimsRows(t *testing.T) {
	driver := newTestDriver(t)
	repo := repository.NewProxyRepository(driver)
	ctx := context.Background()

	now := time.Now().UTC()

	// Seed five inactive rows with distinct ages; index 0 is the oldest.
	for i, minutesAgo := range []int{25, 24, 23, 22, 21} {
		scannedAt := now.Add(-time.Duration(minutesAgo) * time.Minute)
		_, err := driver.Execute(ctx, `
			INSERT INTO proxies (protocol, ip, port, status, last_scanned_at, source, fail_count, created_at, updated_at, first_seen_at)
			VALUES ($1, $2, $3, 'inactive', $4, 'test', 0, $5, $5, $5)`,
			"http", fmt.Sprintf("claim%d.example.com", i), 8080, scannedAt, now)
		if err != nil {
			t.Fatalf("seed inactive %d: %v", i, err)
		}
	}

	// First claim takes the three oldest rows and stamps them as scanned.
	first, err := repo.ListForScan(ctx, 3)
	if err != nil {
		t.Fatalf("ListForScan: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("ListForScan returned %d rows, want 3", len(first))
	}
	// Oldest first: seeds were inserted oldest-first with RESTART IDENTITY,
	// so the three claimed IDs must be exactly 1, 2, 3.
	claimed := map[int64]bool{}
	for _, p := range first {
		claimed[p.ID] = true
		if p.ID != 1 && p.ID != 2 && p.ID != 3 {
			t.Errorf("claimed proxy ID=%d, want one of [1 2 3] (oldest first)", p.ID)
		}
		if p.LastScannedAt == nil || time.Since(*p.LastScannedAt) > time.Minute {
			t.Errorf("claimed proxy ID=%d not stamped at claim time: last_scanned_at=%v", p.ID, p.LastScannedAt)
		}
	}

	// Immediately claiming again must not re-select rows that are in flight.
	second, err := repo.ListForScan(ctx, 5)
	if err != nil {
		t.Fatalf("ListForScan after claim: %v", err)
	}
	if len(second) != 2 {
		t.Fatalf("ListForScan after claim returned %d rows, want 2 (unclaimed remainder)", len(second))
	}
	for _, p := range second {
		if claimed[p.ID] {
			t.Errorf("proxy ID=%d was claimed twice — claim-at-fetch failed", p.ID)
		}
	}
}

func TestProxyRepository_ListForScan_ConcurrentClaimsDoNotOverlap(t *testing.T) {
	driver := newTestDriver(t)
	repo := repository.NewProxyRepository(driver)
	ctx := context.Background()

	now := time.Now().UTC()
	const total = 20
	for i := 0; i < total; i++ {
		scannedAt := now.Add(-time.Duration(30+i) * time.Minute)
		_, err := driver.Execute(ctx, `
			INSERT INTO proxies (protocol, ip, port, status, last_scanned_at, source, fail_count, created_at, updated_at, first_seen_at)
			VALUES ($1, $2, $3, 'inactive', $4, 'test', 0, $5, $5, $5)`,
			"http", fmt.Sprintf("conc%d.example.com", i), 8080, scannedAt, now)
		if err != nil {
			t.Fatalf("seed inactive %d: %v", i, err)
		}
	}

	var mu sync.Mutex
	claimed := map[int64]int{}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				rows, err := repo.ListForScan(ctx, 3)
				if err != nil {
					t.Errorf("ListForScan: %v", err)
					return
				}
				if len(rows) == 0 {
					return
				}
				mu.Lock()
				for _, p := range rows {
					claimed[p.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(claimed) != total {
		t.Errorf("claimed %d distinct proxies, want %d", len(claimed), total)
	}
	for id, n := range claimed {
		if n > 1 {
			t.Errorf("proxy ID=%d claimed %d times concurrently", id, n)
		}
	}
}

func TestProxyRepository_ListDeadForScan_ClaimsRows(t *testing.T) {
	driver := newTestDriver(t)
	repo := repository.NewProxyRepository(driver)
	ctx := context.Background()

	now := time.Now().UTC()

	// Seed three dead rows with distinct ages; index 0 is the oldest.
	for i, daysAgo := range []int{10, 9, 8} {
		scannedAt := now.Add(-time.Duration(daysAgo) * 24 * time.Hour)
		_, err := driver.Execute(ctx, `
			INSERT INTO proxies (protocol, ip, port, status, last_scanned_at, source, fail_count, created_at, updated_at, first_seen_at)
			VALUES ($1, $2, $3, 'dead', $4, 'test', 5, $5, $5, $5)`,
			"http", fmt.Sprintf("deadclaim%d.example.com", i), 8082, scannedAt, now)
		if err != nil {
			t.Fatalf("seed dead %d: %v", i, err)
		}
	}

	first, err := repo.ListDeadForScan(ctx, 2)
	if err != nil {
		t.Fatalf("ListDeadForScan: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("ListDeadForScan returned %d rows, want 2", len(first))
	}
	claimed := map[int64]bool{}
	for _, p := range first {
		claimed[p.ID] = true
		if p.LastScannedAt == nil || time.Since(*p.LastScannedAt) > time.Minute {
			t.Errorf("claimed proxy ID=%d not stamped at claim time: last_scanned_at=%v", p.ID, p.LastScannedAt)
		}
	}

	second, err := repo.ListDeadForScan(ctx, 5)
	if err != nil {
		t.Fatalf("ListDeadForScan after claim: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("ListDeadForScan after claim returned %d rows, want 1 (unclaimed remainder)", len(second))
	}
	if claimed[second[0].ID] {
		t.Errorf("proxy ID=%d was claimed twice — claim-at-fetch failed", second[0].ID)
	}
}
