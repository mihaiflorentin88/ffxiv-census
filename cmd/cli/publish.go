package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish queue jobs (cronjob entrypoint)",
}

var publishIDSweepCmd = &cobra.Command{
	Use:   "id-sweep",
	Short: "Publish id-sweep jobs covering an ID range (chunked)",
	RunE: func(cmd *cobra.Command, args []string) error {
		from, _ := cmd.Flags().GetUint32("from")
		to, _ := cmd.Flags().GetUint32("to")
		chunkSize, _ := cmd.Flags().GetUint32("chunk-size")
		if to < from {
			return fmt.Errorf("--to (%d) must be >= --from (%d)", to, from)
		}
		if chunkSize == 0 {
			chunkSize = 100
		}

		var jobs []contract.QueueJob
		for start := from; start <= to; start += chunkSize {
			end := start + chunkSize - 1
			if end > to {
				end = to
			}
			b, _ := json.Marshal(handler.IDSweepPayload{From: start, To: end})
			jobs = append(jobs, contract.QueueJob{Type: handler.EventIDSweep, Payload: b})
		}

		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue not initialised")
		}
		return q.Publish(cmd.Context(), jobs...)
	},
}

func init() {
	rootCmd.AddCommand(publishCmd)
	publishCmd.AddCommand(publishIDSweepCmd)
	publishIDSweepCmd.Flags().Uint32("from", 1, "first character ID")
	publishIDSweepCmd.Flags().Uint32("to", 0, "last character ID (required)")
	publishIDSweepCmd.Flags().Uint32("chunk-size", 100, "IDs per id-sweep job")
}
