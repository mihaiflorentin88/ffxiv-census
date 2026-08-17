package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census/worker"
)

var consumeCmd = &cobra.Command{
	Use:   "consume <event>",
	Short: "Run a consumer worker for one event type (long-running)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		eventType := args[0]
		concurrency, _ := cmd.Flags().GetInt("concurrency")

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue not initialised")
		}
		w := worker.New(q, container.Load.Handlers(), container.Load.Logger())
		return w.Run(ctx, eventType, concurrency)
	},
}

func init() {
	rootCmd.AddCommand(consumeCmd)
	consumeCmd.Flags().Int("concurrency", 4, "number of concurrent workers")
}
