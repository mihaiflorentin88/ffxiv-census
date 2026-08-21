package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census/worker"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/lodestone"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/provider"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/tomestone"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

var consumeCmd = &cobra.Command{
	Use:   "consume [event]",
	Short: "Run a consumer worker for one or all event queues (long-running)",
	Long: `Run a long-running queue consumer worker.

By default, consumes from all registered event queues concurrently:
  - id-sweep
  - character-census
  - achievement-census

You can specify a single event positional argument or use the --events flag with
a comma-separated list of event names. Rate limits (HTTP 429s) automatically
pause affected provider queues while letting others continue.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		eventsFlag, _ := cmd.Flags().GetString("events")
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		proxyMode, _ := cmd.Flags().GetBool("proxy")

		var eventTypes []string
		if len(args) > 0 && args[0] != "" && args[0] != "all" {
			eventTypes = []string{args[0]}
		} else if eventsFlag != "" && eventsFlag != "all" {
			parts := strings.Split(eventsFlag, ",")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					eventTypes = append(eventTypes, p)
				}
			}
		} else {
			eventTypes = []string{
				handler.EventIDSweep,
				handler.EventCharacterCensus,
				handler.EventAchievementCensus,
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

		if proxyMode {
			return runProxyConsumer(ctx, q, eventTypes, concurrency)
		}

		w := worker.New(q, container.Load.Handlers(), container.Load.Logger(), container.Load.ProviderRateLimiter())
		return w.RunEvents(ctx, eventTypes, concurrency)
	},
}

func init() {
	rootCmd.AddCommand(consumeCmd)
	consumeCmd.AddCommand(consumeFailedCmd)
	consumeCmd.Flags().StringP("events", "e", "all", "comma-separated event types to consume (e.g. id-sweep,character-census or 'all')")
	consumeCmd.Flags().IntP("concurrency", "c", 4, "number of concurrent worker routines")
	consumeCmd.Flags().Bool("proxy", false, "run in proxy mode: each goroutine acquires its own proxy from the pool")
	consumeFailedCmd.Flags().IntP("concurrency", "c", 4, "number of concurrent worker routines")
	consumeFailedCmd.Flags().StringP("events", "e", "all", "comma-separated event types for failed queues (e.g. id-sweep,character-census or 'all')")
}

// runProxyConsumer starts the census consumer in proxy mode. Each worker goroutine
// acquires its own proxy from the ProxyHub and routes ALL requests through it.
func runProxyConsumer(ctx context.Context, q contract.Queue, eventTypes []string, concurrency int) error {
	logger := container.Load.Logger()

	// Read proxy consumer config overrides.
	var proxyLodestoneRate float64
	var proxyRequestTimeout string
	if pcfg := container.Load.Config().Proxy; pcfg != nil {
		proxyLodestoneRate = pcfg.Consumer.LodestoneRateLimit
		proxyRequestTimeout = pcfg.Consumer.RequestTimeout
	}

	// Create proxy-aware client factories.
	newLodestoneClient := func(proxyURL string) (contract.LodestoneClient, error) {
		cfg := container.Load.Config().Lodestone
		if cfg == nil {
			return nil, fmt.Errorf("lodestone config missing")
		}
		// Override rate limit from proxy consumer config if set.
		if proxyLodestoneRate > 0 {
			override := *cfg
			override.RateLimit = proxyLodestoneRate
			return lodestone.NewClientWithProxy(&override, proxyURL, logger)
		}
		return lodestone.NewClientWithProxy(cfg, proxyURL, logger)
	}
	newTomestoneClient := func(proxyURL string) (contract.TomestoneClient, error) {
		cfg := container.Load.Config().Tomestone
		if cfg == nil {
			return nil, fmt.Errorf("tomestone config missing")
		}
		// Override timeout from proxy consumer config if set.
		if proxyRequestTimeout != "" {
			override := *cfg
			override.Timeout = proxyRequestTimeout
			return tomestone.NewClientWithProxy(&override, proxyURL, logger)
		}
		return tomestone.NewClientWithProxy(cfg, proxyURL, logger)
	}
	newRateLimiter := func() contract.ProviderRateLimiter {
		return provider.NewProxyRateLimiter()
	}
	newHandlers := func(lodestoneClient contract.LodestoneClient, tomestoneClient contract.TomestoneClient, rateLimiter contract.ProviderRateLimiter) *handler.Registry {
		return container.Load.ProxyCensusHandlers(lodestoneClient, tomestoneClient, rateLimiter)
	}

	// Get ProxyHub for this process.
	hostname, _ := os.Hostname()
	ownerPrefix := fmt.Sprintf("census-consume-%s-p%d", hostname, os.Getpid())
	proxyHub := container.Load.ProxyHub()
	if proxyHub == nil {
		return fmt.Errorf("proxy hub not initialised (database unavailable?)")
	}

	w := worker.New(q, container.Load.Handlers(), logger)
	return w.RunEventsWithProxy(ctx, eventTypes, concurrency, ownerPrefix, proxyHub, newHandlers, newLodestoneClient, newTomestoneClient, newRateLimiter)
}

var consumeFailedCmd = &cobra.Command{
	Use:   "failed",
	Short: "Consume from failed queues and re-publish to main queues",
	Long: `Run a long-running consumer that reads from per-event-type failed queues
and re-publishes messages back to the main queues for retry. Messages that have
exceeded 100 failure attempts are permanently discarded.

Use --events to filter which failed queues to consume from (e.g. --events "id-sweep,character-census").`,
	RunE: func(cmd *cobra.Command, args []string) error {
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		eventsFlag, _ := cmd.Flags().GetString("events")

		var eventTypes []string
		if eventsFlag != "" && eventsFlag != "all" {
			parts := strings.Split(eventsFlag, ",")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					eventTypes = append(eventTypes, p)
				}
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

		container.Load.Logger().InfoContext(ctx, "consume.failed.start", slog.Int("concurrency", concurrency), slog.Any("event_types", eventTypes))
		return q.ConsumeFailed(ctx, eventTypes, concurrency)
	},
}
