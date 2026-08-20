package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy/handler"
	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy/worker"
)

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Proxy pool management (discover, scan, consume)",
}

var proxyDiscoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Fetch proxies from providers and publish new-proxy events",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue not initialised")
		}
		svc := container.Load.ProxyService()
		if svc == nil {
			return fmt.Errorf("proxy service not initialised")
		}

		providers := svc.Providers()
		if len(providers) == 0 {
			return fmt.Errorf("no proxy providers configured")
		}

		totalDiscovered := 0
		totalPublished := 0
		totalSkipped := 0

		for _, p := range providers {
			proxies, err := p.FetchProxies(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: provider %s failed: %v\n", p.Name(), err)
				continue
			}
			totalDiscovered += len(proxies)

			for _, rec := range proxies {
				job := handler.NewProxyJob(handler.NewProxyPayload{
					Protocol:      rec.Protocol,
					IP:            rec.IP,
					Port:          rec.Port,
					Country:       rec.Country,
					Anonymity:     rec.Anonymity,
					Source:        rec.Source,
					UptimePercent: rec.UptimePercent,
				})
				n, err := q.Publish(ctx, job)
				if err != nil {
					return fmt.Errorf("publish new-proxy event: %w", err)
				}
				if n > 0 {
					totalPublished++
				} else {
					totalSkipped++
				}
			}
		}

		fmt.Printf("discovered=%d published=%d skipped_dedup=%d\n", totalDiscovered, totalPublished, totalSkipped)
		return nil
	},
}

var proxyScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Publish scan-proxy events for proxies needing verification",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue not initialised")
		}
		repo := container.Load.ProxyRepository()
		if repo == nil {
			return fmt.Errorf("proxy repository not initialised")
		}

		limit, _ := cmd.Flags().GetInt("limit")
		if limit <= 0 {
			cfg := container.Load.Config().Proxy
			if cfg != nil && cfg.ScanBatchSize > 0 {
				limit = cfg.ScanBatchSize
			} else {
				limit = 50
			}
		}

		proxies, err := repo.ListForScan(ctx, limit)
		if err != nil {
			return fmt.Errorf("list proxies for scan: %w", err)
		}

		published := 0
		for _, p := range proxies {
			job := handler.ScanProxyJob(p.ID)
			n, err := q.Publish(ctx, job)
			if err != nil {
				return fmt.Errorf("publish scan-proxy event: %w", err)
			}
			if n > 0 {
				published++
			}
		}

		fmt.Printf("scannable=%d published=%d\n", len(proxies), published)
		return nil
	},
}

var proxyConsumeCmd = &cobra.Command{
	Use:   "consume",
	Short: "Run a consumer worker for proxy event queues (long-running)",
	RunE: func(cmd *cobra.Command, args []string) error {
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		pollIntervalStr, _ := cmd.Flags().GetString("poll-interval")

		pollInterval := 500 * time.Millisecond
		if pollIntervalStr != "" {
			if d, err := time.ParseDuration(pollIntervalStr); err == nil && d > 0 {
				pollInterval = d
			}
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue not initialised")
		}
		w := worker.New(q, container.Load.ProxyHandlers(), container.Load.Logger())
		w.SetPollInterval(pollInterval)

		eventTypes := []string{
			handler.EventNewProxy,
			handler.EventScanProxy,
		}
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
	proxyConsumeCmd.Flags().String("poll-interval", "500ms", "idle queue polling interval (e.g. 500ms, 1s)")
}
