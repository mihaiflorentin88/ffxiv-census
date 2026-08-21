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
func publishDiscoveredProxies(ctx context.Context, q contract.Queue, repo contract.ProxyRepository, logger contract.Logger, providers []contract.ProxyProvider) (int, error) {
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
			return nil
		})
		if err != nil {
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

		published, err := publishDiscoveredProxies(ctx, q, repo, logger, providers)
		if err != nil {
			return err
		}

		logger.InfoContext(ctx, "proxy.discover.complete", "published", published)
		return nil
	},
}

var proxyScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Publish scan-proxy events for proxies needing verification",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logger := container.Load.Logger()

		logger.InfoContext(ctx, "proxy.scan.start")

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

		counts, _ := repo.CountByStatus(ctx)
		logger.InfoContext(ctx, "proxy.scan.db_stats", "active", counts["active"], "inactive", counts["inactive"], "dead", counts["dead"], "total", counts["active"]+counts["inactive"]+counts["dead"])

		limit, _ := cmd.Flags().GetInt("limit")
		if limit < 0 {
			limit = 0
		}
		if limit == 0 {
			logger.InfoContext(ctx, "proxy.scan.querying", "limit", "all")
		} else {
			logger.InfoContext(ctx, "proxy.scan.querying", "limit", limit)
		}

		start := time.Now()
		proxies, err := repo.ListForScan(ctx, limit)
		if err != nil {
			return fmt.Errorf("list proxies for scan: %w", err)
		}
		logger.InfoContext(ctx, "proxy.scan.found", "scannable", len(proxies), "duration", time.Since(start))

		if len(proxies) == 0 {
			logger.InfoContext(ctx, "proxy.scan.complete", "published", 0, "reason", "no proxies need scanning")
			return nil
		}

		var inactive, active, dead int
		for _, p := range proxies {
			switch p.Status {
			case "inactive":
				inactive++
			case "active":
				active++
			case "dead":
				dead++
			}
		}
		logger.InfoContext(ctx, "proxy.scan.priority_breakdown", "inactive", inactive, "active_stale", active, "dead_rescan", dead)

		var jobs []contract.QueueJob
		for _, p := range proxies {
			jobs = append(jobs, handler.ScanProxyJob(p.ID))
		}

		confirmed, err := publishAll(q, logger, ctx, jobs)
		if err != nil {
			return err
		}

		logger.InfoContext(ctx, "proxy.scan.complete", "scannable", len(proxies), "published", confirmed)
		return nil
	},
}

var proxyConsumeCmd = &cobra.Command{
	Use:   "consume [event-type]",
	Short: "Run a consumer worker for proxy event queues (long-running)",
	Long: `Run a long-running proxy queue consumer worker.

By default, consumes from both new-proxy and scan-proxy queues.
Specify a single event type as a positional argument to consume only that queue.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		concurrency, _ := cmd.Flags().GetInt("concurrency")

		var eventTypes []string
		if len(args) > 0 && args[0] != "" {
			eventTypes = []string{args[0]}
		} else {
			eventTypes = []string{
				handler.EventNewProxy,
				handler.EventScanProxy,
			}
		}

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
		return w.RunEvents(ctx, eventTypes, concurrency)
	},
}

func init() {
	rootCmd.AddCommand(proxyCmd)

	proxyCmd.AddCommand(proxyDiscoverCmd)
	proxyCmd.AddCommand(proxyScanCmd)
	proxyCmd.AddCommand(proxyConsumeCmd)

	proxyScanCmd.Flags().IntP("limit", "l", 0, "max proxies to queue for scanning (0 = no limit, scan all)")
	proxyConsumeCmd.Flags().IntP("concurrency", "c", 4, "number of concurrent worker routines")
}
