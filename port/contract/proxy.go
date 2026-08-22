package contract

import (
	"context"
	"time"
)

// Proxy status constants.
const (
	ProxyStatusActive   = "active"
	ProxyStatusInactive = "inactive"
	ProxyStatusDead     = "dead"
)

// ProxyRecord represents a proxy endpoint stored in the database.
type ProxyRecord struct {
	ID            int64
	Protocol      string // "http", "https", "socks4", "socks5"
	IP            string
	Port          int
	Country       *string
	Anonymity     *string // "elite", "anonymous", "transparent"
	LatencyMS     *int
	UptimePercent *float64
	Status        string // "active", "inactive", "dead"
	LastScannedAt *time.Time
	LastAliveAt   *time.Time
	FirstSeenAt   time.Time
	Source        string // provider name: "proxyscrape", "geonode", etc.
	FailCount     int
	LockedBy      *string    // process name + goroutine ID (e.g. "census-consume-g3")
	LockedAt      *time.Time // lock acquisition time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ProxyChecker tests whether a proxy endpoint is reachable and measures latency.
type ProxyChecker interface {
	Check(ctx context.Context, protocol, ip string, port int) (latencyMS int, err error)
}

// ProxyProvider fetches proxy lists from an external source.
type ProxyProvider interface {
	// Name returns the provider identifier (e.g. "proxyscrape", "geonode").
	Name() string
	// FetchProxies streams proxy records from the provider, calling emit for each
	// one. If emit returns an error, FetchProxies stops and returns it immediately.
	FetchProxies(ctx context.Context, emit func(ProxyRecord) error) error
}

// ProxyRepository persists proxy records.
type ProxyRepository interface {
	// Exists returns true if a proxy with the exact (protocol, ip, port) tuple
	// already exists in the database. Read-only check, no side effects.
	Exists(ctx context.Context, protocol, ip string, port int) (bool, error)
	// InsertIfAbsent inserts a new proxy only if no row with the same
	// (protocol, ip, port) exists. Returns the proxy ID, whether it was
	// actually inserted, and any error. On conflict, returns (0, false, nil)
	// without modifying the existing row.
	InsertIfAbsent(ctx context.Context, rec ProxyRecord) (id int64, inserted bool, err error)
	// Get returns a proxy by ID, or nil (no error) if not found.
	Get(ctx context.Context, id int64) (*ProxyRecord, error)
	// UpdateStatus updates a proxy's status, latency, fail_count, and last_alive_at.
	UpdateStatus(ctx context.Context, id int64, status string, latencyMS *int, failCount int, lastAliveAt *time.Time) error
	// UpdateScanTime sets last_scanned_at and updated_at to now.
	UpdateScanTime(ctx context.Context, id int64) error
	// ListForScan returns eligible inactive and active proxies needing verification,
	// ordered by scan priority: inactive (oldest scan first), then active not scanned
	// in 10 minutes. Dead proxies are excluded — use ListDeadForScan for those.
	ListForScan(ctx context.Context, limit int) ([]ProxyRecord, error)
	// ListDeadForScan returns eligible dead proxies (not scanned in 7 days),
	// ordered by oldest scan first. Only dead proxies are returned.
	ListDeadForScan(ctx context.Context, limit int) ([]ProxyRecord, error)
	// ListActive returns up to limit active proxies ordered by latency (lowest first).
	ListActive(ctx context.Context, limit int) ([]ProxyRecord, error)
	// Count returns the total number of proxies.
	Count(ctx context.Context) (int64, error)
	// CountByStatus returns proxy counts grouped by status.
	CountByStatus(ctx context.Context) (map[string]int64, error)
	// ClaimProxy atomically claims an available proxy for the given owner using
	// FOR UPDATE SKIP LOCKED. Returns nil (no error) if no proxy is available.
	// The proxy must be active, not currently locked, or locked past its TTL.
	// Only proxies with protocols http, https, socks4, socks5 are considered.
	ClaimProxy(ctx context.Context, owner string, lockTTL time.Duration) (*ProxyRecord, error)
	// ExtendLock extends the lock TTL for a proxy owned by the given owner.
	// Returns false if the proxy is not owned by the caller.
	ExtendLock(ctx context.Context, id int64, owner string, lockTTL time.Duration) (bool, error)
	// ReleaseProxy releases the lock on a proxy owned by the given owner.
	ReleaseProxy(ctx context.Context, id int64, owner string) error
	// MarkFailedProxy atomically releases the lock AND sets the proxy to inactive
	// with an incremented fail count. This prevents a TOCTOU race where another
	// worker could claim the proxy between Release and UpdateStatus.
	MarkFailedProxy(ctx context.Context, id int64, owner string) error
	// RandomActive returns a random active proxy, optionally excluding IDs.
	// Returns nil (no error) if no eligible proxy exists. Does NOT claim or lock
	// the proxy — used only for discovery/provider scraping, never for Lodestone.
	RandomActive(ctx context.Context, excludeIDs []int64) (*ProxyRecord, error)
}
