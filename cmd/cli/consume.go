package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census/worker"
)

var consumeCmd = &cobra.Command{
	Use:   "consume [event]",
	Short: "Run a consumer worker for one or all event queues (long-running)",
	Long: `Run a long-running queue consumer worker.

By default, consumes from all registered event queues concurrently:
  - id-sweep
  - character-census
  - achievement-census
  - fc-census

You can specify a single event positional argument or use the --events flag with
a comma-separated list of event names. Rate limits (HTTP 429s) automatically
pause affected provider queues while letting others continue.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		eventsFlag, _ := cmd.Flags().GetString("events")
		concurrency, _ := cmd.Flags().GetInt("concurrency")
		pollIntervalStr, _ := cmd.Flags().GetString("poll-interval")

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
				handler.EventFreeCompanyCensus,
			}
		}

		pollInterval := time.Second
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
		w := worker.New(q, container.Load.Handlers(), container.Load.Logger(), container.Load.ProviderRateLimiter())
		w.SetPollInterval(pollInterval)
		return w.RunEvents(ctx, eventTypes, concurrency)
	},
}

func init() {
	rootCmd.AddCommand(consumeCmd)
	consumeCmd.Flags().StringP("events", "e", "all", "comma-separated event types to consume (e.g. id-sweep,character-census or 'all')")
	consumeCmd.Flags().IntP("concurrency", "c", 4, "number of concurrent worker routines")
	consumeCmd.Flags().String("poll-interval", "500ms", "idle queue polling interval (e.g. 500ms, 1s)")
}
