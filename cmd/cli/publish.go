package cli

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

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
		for start := from; start <= to; {
			end := start + chunkSize - 1
			if end > to || end < start {
				end = to // last chunk, or uint32 overflow guard
			}
			b, _ := json.Marshal(handler.IDSweepPayload{From: start, To: end})
			jobs = append(jobs, contract.QueueJob{Type: handler.EventIDSweep, Payload: b})
			if end == to {
				break
			}
			start = end + 1
		}

		container.Load.Logger().InfoContext(cmd.Context(), "publish.id_sweep", slog.Uint64("from", uint64(from)), slog.Uint64("to", uint64(to)), slog.Uint64("chunk_size", uint64(chunkSize)), slog.Int("jobs", len(jobs)))
		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue not initialised")
		}
		return q.Publish(cmd.Context(), jobs...)
	},
}

var publishCharacterCensusCmd = &cobra.Command{
	Use:   "character-census",
	Short: "Publish character-census jobs for stale characters (recheck)",
	RunE: func(cmd *cobra.Command, args []string) error {
		olderThan, _ := cmd.Flags().GetDuration("older-than")
		limit, _ := cmd.Flags().GetInt("limit")
		if olderThan <= 0 {
			return fmt.Errorf("--older-than must be positive")
		}
		if limit <= 0 {
			return fmt.Errorf("--limit must be positive")
		}
		repo := container.Load.CharacterRepository()
		if repo == nil {
			return fmt.Errorf("character repository not initialised")
		}
		cutoff := time.Now().UTC().Add(-olderThan)
		stale, err := repo.ListStale(cmd.Context(), cutoff, limit)
		if err != nil {
			return fmt.Errorf("list stale: %w", err)
		}
		var jobs []contract.QueueJob
		for _, c := range stale {
			jobs = append(jobs, handler.CharacterCensusJob(c.ID))
		}
		container.Load.Logger().InfoContext(cmd.Context(), "publish.character_census", slog.String("older_than", olderThan.String()), slog.Int("limit", limit), slog.Int("stale", len(stale)))
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
	publishCmd.AddCommand(publishCharacterCensusCmd)
	publishCharacterCensusCmd.Flags().Duration("older-than", 720*time.Hour, "only re-census characters not seen within this duration")
	publishCharacterCensusCmd.Flags().Int("limit", 1000, "max characters to enqueue")
}
