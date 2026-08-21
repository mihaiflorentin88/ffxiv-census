package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy/handler"
	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy/worker"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Proxy pool management (discover, scan, consume)",
}

// publishDiscoveredProxies fetches proxies from each provider sequentially,
// builds jobs for one provider at a time, and publishes them via publishAll.
// It continues past individual provider fetch failures but returns an error
// when no provider publishes anything and at least one failed.
func publishDiscoveredProxies(ctx context.Context, q contract.Queue, logger contract.Logger, providers []contract.ProxyProvider) (int, error) {
	totalPublished := 0
	totalErrors := 0

	for _, p := range providers {
		logger.InfoContext(ctx, "proxy.discover.fetching", "provider", p.Name())
		start := time.Now()
		proxies, err := p.FetchProxies(ctx)
		if err != nil {
			logger.ErrorContext(ctx, "proxy.discover.provider_failed", "provider", p.Name(), "error", err, "duration", time.Since(start))
			totalErrors++
			continue
		}
		logger.InfoContext(ctx, "proxy.discover.provider_done", "provider", p.Name(), "fetched", len(proxies), "duration", time.Since(start))

		var jobs []contract.QueueJob
		for _, rec := range proxies {
			jobs = append(jobs, handler.NewProxyJob(handler.NewProxyPayload{
				Protocol:      rec.Protocol,
				IP:            rec.IP,
				Port:          rec.Port,
				Country:       rec.Country,
				Anonymity:     rec.Anonymity,
				Source:        rec.Source,
				UptimePercent: rec.UptimePercent,
			}))
		}

		if len(jobs) > 0 {
			confirmed, err := publishAll(q, logger, ctx, jobs)
			totalPublished += confirmed
			if err != nil {
				logger.ErrorContext(ctx, "proxy.discover.publish_failed", "provider", p.Name(), "error", err)
				totalErrors++
			}
		}
		logger.InfoContext(ctx, "proxy.discover.provider_published", "provider", p.Name(), "published", len(jobs))

		// Release proxy data for this provider before fetching the next one.
		proxies = nil
		runtime.GC()
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

		svc := container.Load.ProxyService()
		if svc == nil {
			return fmt.Errorf("proxy service not initialised")
		}

		providers := svc.Providers()
		if len(providers) == 0 {
			return fmt.Errorf("no proxy providers configured")
		}
		logger.InfoContext(ctx, "proxy.discover.providers", "count", len(providers))

		published, err := publishDiscoveredProxies(ctx, q, logger, providers)
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
		if limit <= 0 {
			cfg := container.Load.Config().Proxy
			if cfg != nil && cfg.ScanBatchSize > 0 {
				limit = cfg.ScanBatchSize
			} else {
				limit = 50
			}
		}
		logger.InfoContext(ctx, "proxy.scan.querying", "limit", limit)

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

	proxyScanCmd.Flags().IntP("limit", "l", 50, "max proxies to queue for scanning")
	proxyConsumeCmd.Flags().IntP("concurrency", "c", 4, "number of concurrent worker routines")
}
