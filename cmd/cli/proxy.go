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

var proxyScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Run a long-running proxy scan worker (direct database batches)",
	Long: `Run a long-running scan worker that reads priority-ordered proxy batches
directly from the database and scans them concurrently. Concurrency controls
both the goroutine limit per batch and the SQL LIMIT. Waits one minute after
empty batches or per-record errors before querying again.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		concurrency, _ := cmd.Flags().GetInt("concurrency")

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		logger := container.Load.Logger()
		logger.InfoContext(ctx, "proxy.scan.start", "concurrency", concurrency)

		repo := container.Load.ProxyRepository()
		if repo == nil {
			return fmt.Errorf("proxy repository not initialised")
		}

		svc := container.Load.ProxyService()
		if svc == nil {
			return fmt.Errorf("proxy service not initialised")
		}

		scanWorker := worker.NewScanWorker(repo, svc, logger, time.Minute)
		return scanWorker.RunScan(ctx, concurrency)
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
	proxyConsumeCmd.Flags().IntP("concurrency", "c", 4, "number of concurrent worker routines")
}
