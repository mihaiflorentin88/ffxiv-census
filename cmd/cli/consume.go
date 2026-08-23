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
	"golang.org/x/time/rate"

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

		logger := container.Load.Logger()

		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue not initialised")
		}
		defer func() {
			if err := q.Close(); err != nil {
				logger.ErrorContext(ctx, "queue.close_error", slog.Any("error", err))
			}
		}()

		if proxyMode {
			return runProxyConsumer(ctx, q, eventTypes, concurrency)
		}

		w := worker.New(q, container.Load.Handlers(), logger, container.Load.ProviderRateLimiter())
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

	// Read all config once — immutable for the lifetime of this function.
	cfg := container.Load.Config()
	lodestoneCfg := cfg.Lodestone
	tomestoneCfg := cfg.Tomestone

	var proxyLodestoneRate float64
	var proxyRequestTimeout string
	if pcfg := cfg.Proxy; pcfg != nil {
		proxyLodestoneRate = pcfg.Consumer.LodestoneRateLimit
		proxyRequestTimeout = pcfg.Consumer.RequestTimeout
	}

	// Prewarm CensusService on the command goroutine so milestone sync and
	// service construction happen once, before goroutines call ProxyCensusHandlers.
	if svc := container.Load.CensusService(); svc == nil {
		return fmt.Errorf("census service not initialised")
	}

	// Create proxy-aware client factories.
	newLodestoneClient := func(proxyURL string, limiter contract.ProviderRateLimiter) (contract.LodestoneClient, error) {
		if lodestoneCfg == nil {
			return nil, fmt.Errorf("lodestone config missing")
		}
		// Override rate limit from proxy consumer config if set.
		if proxyLodestoneRate > 0 {
			override := *lodestoneCfg
			override.RateLimit = proxyLodestoneRate
			return lodestone.NewCustomClient(&override, logger, limiter, lodestone.WithProxy(proxyURL))
		}
		return lodestone.NewCustomClient(lodestoneCfg, logger, limiter, lodestone.WithProxy(proxyURL))
	}

	// Create a shared rate limiter for all proxy Tomestone clients.
	var sharedTomestoneLimiter *rate.Limiter
	if tomestoneCfg != nil {
		r := tomestoneCfg.RateLimit
		if r <= 0 {
			r = 10.0
		}
		sharedTomestoneLimiter = rate.NewLimiter(rate.Limit(r), 1)
	}

	// Achievement-only proxy processes can omit the unused Tomestone transport.
	needTomestone := proxyEventsNeedTomestone(eventTypes)

	var newTomestoneClient func(string, contract.ProviderRateLimiter) (contract.TomestoneClient, error)
	if needTomestone && tomestoneCfg != nil {
		newTomestoneClient = func(proxyURL string, limiter contract.ProviderRateLimiter) (contract.TomestoneClient, error) {
			opts := []tomestone.ClientOption{
				tomestone.WithProviderRateLimiter(limiter),
			}
			if sharedTomestoneLimiter != nil {
				opts = append(opts, tomestone.WithSharedRateLimiter(sharedTomestoneLimiter))
			}
			// Override timeout from proxy consumer config if set.
			if proxyRequestTimeout != "" {
				override := *tomestoneCfg
				override.Timeout = proxyRequestTimeout
				return tomestone.NewClientWithProxy(&override, proxyURL, logger, opts...)
			}
			return tomestone.NewClientWithProxy(tomestoneCfg, proxyURL, logger, opts...)
		}
	} else {
		newTomestoneClient = func(string, contract.ProviderRateLimiter) (contract.TomestoneClient, error) {
			return nil, nil
		}
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

	// Pass nil handlers — RunEventsWithProxy uses the per-goroutine registry
	// returned by newHandlers, never reads w.handlers.
	w := worker.New(q, nil, logger)
	return w.RunEventsWithProxy(ctx, eventTypes, concurrency, ownerPrefix, proxyHub, newHandlers, newLodestoneClient, newTomestoneClient, newRateLimiter)
}

// proxyEventsNeedTomestone returns true if the selected event set requires a
// Tomestone client. Achievement-census is Lodestone-only, so an achievement-only
// proxy process can skip creating unused Tomestone transports.
func proxyEventsNeedTomestone(eventTypes []string) bool {
	if len(eventTypes) == 0 {
		return true
	}
	for _, eventType := range eventTypes {
		switch eventType {
		case handler.EventAchievementCensus:
			continue
		default:
			return true
		}
	}
	return false
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

		logger := container.Load.Logger()

		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue not initialised")
		}
		defer func() {
			if err := q.Close(); err != nil {
				logger.ErrorContext(ctx, "queue.close_error", slog.Any("error", err))
			}
		}()

		logger.InfoContext(ctx, "consume.failed.start", slog.Int("concurrency", concurrency), slog.Any("event_types", eventTypes))
		return q.ConsumeFailed(ctx, eventTypes, concurrency)
	},
}
