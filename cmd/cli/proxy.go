package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy/handler"
	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy/worker"
	proxyinfra "github.com/mihaiflorentin88/ffxiv-census/infrastructure/proxy"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// errPublishFailed wraps a queue publish error so callers can distinguish
// emit/publish failures from provider fetch/decode errors.
var errPublishFailed = errors.New("publish failed")

// errDiscoveryLimitReached is returned when the successful-publication limit
// has been reached. It signals successful completion, not an error.
var errDiscoveryLimitReached = errors.New("discovery limit reached")

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Proxy pool management (discover, scan, consume)",
}

// errLookupFailed wraps a repository lookup error so callers can distinguish
// lookup failures from publish failures.
var errLookupFailed = errors.New("proxy lookup failed")

// publishDiscoveredProxies fetches proxies from each provider sequentially,
// publishing each record to the queue as it is emitted by the provider.
// It checks the repository for existing tuples before publishing.
// It continues past individual provider failures but returns an error
// when no provider publishes anything and at least one failed.
func publishDiscoveredProxies(ctx context.Context, q contract.Queue, repo contract.ProxyRepository, logger contract.Logger, providers []contract.ProxyProvider, limit int) (int, error) {
	if limit < 0 {
		limit = 0
	}
	totalPublished := 0
	totalErrors := 0

	for _, p := range providers {
		logger.InfoContext(ctx, "proxy.discover.fetching", "provider", p.Name())
		start := time.Now()
		publishedForProvider := 0
		skippedExistingForProvider := 0

		err := p.FetchProxies(ctx, func(rec contract.ProxyRecord) error {
			exists, lookupErr := repo.Exists(ctx, rec.Protocol, rec.IP, rec.Port)
			if lookupErr != nil {
				return fmt.Errorf("%w: %w", errLookupFailed, lookupErr)
			}
			if exists {
				skippedExistingForProvider++
				return nil
			}
			job := handler.NewProxyJob(handler.NewProxyPayload{
				Protocol:      rec.Protocol,
				IP:            rec.IP,
				Port:          rec.Port,
				Country:       rec.Country,
				Anonymity:     rec.Anonymity,
				Source:        rec.Source,
				UptimePercent: rec.UptimePercent,
			})
			if err := q.Publish(ctx, job); err != nil {
				return fmt.Errorf("%w: %w", errPublishFailed, err)
			}
			publishedForProvider++
			totalPublished++
			if limit > 0 && totalPublished >= limit {
				return errDiscoveryLimitReached
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, errDiscoveryLimitReached) {
				logger.InfoContext(ctx, "proxy.discover.limit_reached", "provider", p.Name(), "published", publishedForProvider, "skipped_existing", skippedExistingForProvider, "total", totalPublished, "duration", time.Since(start))
				return totalPublished, nil
			}
			if errors.Is(err, errLookupFailed) {
				logger.ErrorContext(ctx, "proxy.discover.lookup_failed", "provider", p.Name(), "error", err, "duration", time.Since(start))
			} else if errors.Is(err, errPublishFailed) {
				logger.ErrorContext(ctx, "proxy.discover.publish_failed", "provider", p.Name(), "error", err, "duration", time.Since(start))
			} else {
				logger.ErrorContext(ctx, "proxy.discover.provider_failed", "provider", p.Name(), "error", err, "duration", time.Since(start))
			}
			totalErrors++
			continue
		}
		logger.InfoContext(ctx, "proxy.discover.provider_done", "provider", p.Name(), "published", publishedForProvider, "skipped_existing", skippedExistingForProvider, "duration", time.Since(start))
	}

	if totalPublished == 0 && totalErrors > 0 {
		return 0, fmt.Errorf("proxy discovery failed: all providers failed (%d errors)", totalErrors)
	}
	return totalPublished, nil
}

var proxyDiscoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Fetch proxies from providers and publish new-proxy events",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logger := container.Load.Logger()

		logger.InfoContext(ctx, "proxy.discover.start")

		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue not initialised")
		}
		defer func() {
			if err := q.Close(); err != nil {
				logger.ErrorContext(ctx, "queue.close_error", slog.Any("error", err))
			}
		}()

		repo := container.Load.ProxyRepository()
		if repo == nil {
			return fmt.Errorf("proxy repository not initialised")
		}

		svc := container.Load.ProxyService()
		if svc == nil {
			return fmt.Errorf("proxy service not initialised")
		}

		providers := svc.Providers()
		if len(providers) == 0 {
			return fmt.Errorf("no proxy providers configured")
		}
		logger.InfoContext(ctx, "proxy.discover.providers", "count", len(providers))

		limit, _ := cmd.Flags().GetInt("limit")
		if limit < 0 {
			limit = 0
		}

		published, err := publishDiscoveredProxies(ctx, q, repo, logger, providers, limit)
		if err != nil {
			return err
		}

		logger.InfoContext(ctx, "proxy.discover.complete", "published", published)
		return nil
	},
}

// scanWeights reserves long-run capacity shares for verification, recovery
// and background scanning. Task 6 replaces these literals with parsed
// [proxy.scan] configuration.
var scanWeights = [3]int{10, 45, 45}

// The guard and general-availability literals below move into the parsed
// [proxy.scan] configuration in Task 6.
const (
	generalAvailabilityURL = "https://api64.ipify.org?format=json"
	generalCheckTimeout    = 10 * time.Second
	guardControlInterval   = 30 * time.Second
)

// defaultProxyScanPolicy is the scheduler policy the scanner runs with until
// Task 6 wires the parsed configuration through.
func defaultProxyScanPolicy() contract.ProxyScanPolicy {
	return contract.ProxyScanPolicy{
		VerifyInterval:    time.Minute,
		FreshnessTTL:      2 * time.Minute,
		RecoveryBase:      time.Minute,
		RecoveryCap:       15 * time.Minute,
		RecoveryHorizon:   time.Hour,
		LeaseDuration:     30 * time.Second,
		MinRepeatInterval: time.Second,
		InconclusiveRetry: time.Minute,
		DeadAfter:         48 * time.Hour,
		FailThreshold:     5,
	}
}

// generalAvailabilityCheck binds the guard's control callback to a strict
// direct egress validation: no proxy, complete JSON response required.
func generalAvailabilityCheck(logger contract.Logger) func(context.Context) error {
	health := proxyinfra.NewHealthChecker(generalAvailabilityURL, generalCheckTimeout, logger)
	return health.CheckDirect
}

// runScanWorker wires the scan store, checker and endpoint guard and runs
// the scan worker until the context is cancelled.
func runScanWorker(ctx context.Context, concurrency, deadScanPercentage int) error {
	logger := container.Load.Logger()
	logger.InfoContext(ctx, "proxy.scan.start",
		"concurrency", concurrency,
		"dead_scan_percentage", deadScanPercentage)

	repo := container.Load.ProxyRepository()
	if repo == nil {
		return fmt.Errorf("proxy repository not initialised")
	}
	store, ok := repo.(contract.ProxyScanStore)
	if !ok {
		return fmt.Errorf("proxy repository does not support the scan store contract")
	}

	guard := proxyinfra.NewEndpointGuard(generalAvailabilityCheck(logger), guardControlInterval, logger)
	guardDone := make(chan error, 1)
	go func() {
		guardDone <- guard.Run(ctx)
	}()

	scanWorker := worker.NewScanWorker(store, container.Load.ProxyChecker(), guard,
		defaultProxyScanPolicy(), scanWeights, logger)
	if hub := container.Load.ProxyHub(); hub != nil {
		scanWorker.SetNotifier(hub.NotifyAvailable)
	}

	err := scanWorker.RunScan(ctx, concurrency)
	// RunScan returns only on cancellation, so the guard has observed the
	// same cancellation and its Run loop is winding down.
	if guardErr := <-guardDone; guardErr != nil {
		logger.ErrorContext(ctx, "proxy_scan.guard_exit", slog.Any("error", guardErr))
	}
	return err
}

var proxyScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Run a long-running proxy scan worker (guarded, lease-based)",
	Long: `Run a long-running scan worker that leases due proxy rows from the
verification, recovery and background queues and scans them within the
configured concurrency. Claims are bounded by free capacity and a claim
timeout shorter than the lease; the endpoint guard pauses claims while this
replica's own egress is unhealthy.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		deadScanPercentage, _ := cmd.Flags().GetInt("dead-scan-percentage")

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		return runScanWorker(ctx, concurrency, deadScanPercentage)
	},
}

var proxyConsumeCmd = &cobra.Command{
	Use:   "consume",
	Short: "Run a consumer worker for the new-proxy queue (long-running)",
	Long: `Run a long-running proxy queue consumer worker for the new-proxy event.
Scans are performed directly by the proxy scan worker, not via the queue.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		concurrency, _ := cmd.Flags().GetInt("concurrency")

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue not initialised")
		}
		defer func() {
			if err := q.Close(); err != nil {
				container.Load.Logger().ErrorContext(ctx, "queue.close_error", slog.Any("error", err))
			}
		}()

		w := worker.New(q, container.Load.ProxyHandlers(), container.Load.Logger())
		return w.RunEvents(ctx, []string{handler.EventNewProxy}, concurrency)
	},
}

func init() {
	rootCmd.AddCommand(proxyCmd)

	proxyCmd.AddCommand(proxyDiscoverCmd)
	proxyCmd.AddCommand(proxyScanCmd)
	proxyCmd.AddCommand(proxyConsumeCmd)

	proxyDiscoverCmd.Flags().IntP("limit", "l", 0, "max proxies to publish after deduplication (0 = no limit)")
	proxyScanCmd.Flags().IntP("concurrency", "c", 4, "number of concurrent scan routines (also used as SQL batch limit)")
	proxyScanCmd.Flags().Int("dead-scan-percentage", 0, "percentage of scan concurrency reserved for dead proxies (0-90; values above 90 are capped)")
	proxyConsumeCmd.Flags().IntP("concurrency", "c", 4, "number of concurrent worker routines")
}
