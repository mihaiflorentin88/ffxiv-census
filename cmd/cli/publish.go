package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
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

func computeIDSweepRange(from, to, count uint32, auto bool, maxIDFunc func() (uint32, error)) (uint32, uint32, error) {
	if count == 0 {
		return 0, 0, fmt.Errorf("--count / --batch-size must be > 0")
	}
	if auto || (from == 0 && to == 0) {
		if maxIDFunc == nil {
			return 0, 0, fmt.Errorf("maxIDFunc required when auto-discovering ID sweep range")
		}
		maxID, err := maxIDFunc()
		if err != nil {
			return 0, 0, fmt.Errorf("lookup max character id: %w", err)
		}
		from = maxID + 1
		to = maxID + count
	} else if from > 0 && to == 0 {
		to = from + count - 1
	} else if to > 0 && from == 0 {
		from = 1
	}
	if to < from {
		return 0, 0, fmt.Errorf("--to (%d) must be >= --from (%d)", to, from)
	}
	return from, to, nil
}

func buildIDSweepJobs(from, to, chunkSize uint32, source string) []contract.QueueJob {
	if chunkSize == 0 {
		chunkSize = 100
	}
	var jobs []contract.QueueJob
	for start := from; start <= to; {
		end := start + chunkSize - 1
		if end > to || end < start {
			end = to // last chunk, or uint32 overflow guard
		}
		b, _ := json.Marshal(handler.IDSweepPayload{From: start, To: end, Source: source})
		jobs = append(jobs, contract.QueueJob{
			Type:    handler.EventIDSweep,
			Payload: b,
		})
		if end == to {
			break
		}
		start = end + 1
	}
	return jobs
}

func buildGapSweepJobs(gaps [][2]uint32, chunkSize uint32, source string) []contract.QueueJob {
	var jobs []contract.QueueJob
	for _, g := range gaps {
		gapJobs := buildIDSweepJobs(g[0], g[1], chunkSize, source)
		jobs = append(jobs, gapJobs...)
	}
	return jobs
}

func publishAll(q contract.Queue, logger contract.Logger, ctx context.Context, jobs []contract.QueueJob) (int, error) {
	for i, job := range jobs {
		if err := q.Publish(ctx, job); err != nil {
			return i, fmt.Errorf("publish %q job %d of %d: %w", job.Type, i+1, len(jobs), err)
		}
	}
	logger.InfoContext(ctx, "publish.enqueued", slog.Int("count", len(jobs)))
	return len(jobs), nil
}

var publishIDSweepCmd = &cobra.Command{
	Use:   "id-sweep",
	Short: "Publish id-sweep jobs covering an ID range (chunked, auto-discovery, gap-fill, daemon)",
	RunE: func(cmd *cobra.Command, args []string) error {
		auto, _ := cmd.Flags().GetBool("auto")
		from, _ := cmd.Flags().GetUint32("from")
		to, _ := cmd.Flags().GetUint32("to")
		count, _ := cmd.Flags().GetUint32("count")
		batchSize, _ := cmd.Flags().GetUint32("batch-size")
		if cmd.Flags().Changed("batch-size") {
			count = batchSize
		}
		chunkSize, _ := cmd.Flags().GetUint32("chunk-size")
		source, _ := cmd.Flags().GetString("source")
		fillGaps, _ := cmd.Flags().GetBool("fill-gaps")
		daemon, _ := cmd.Flags().GetBool("daemon")
		daemonInterval, _ := cmd.Flags().GetDuration("daemon-interval")
		maxGaps, _ := cmd.Flags().GetInt("max-gaps")
		if daemon && daemonInterval <= 0 {
			return fmt.Errorf("invalid --daemon-interval %v: must be positive", daemonInterval)
		}
		switch source {
		case "auto", "tomestone", "lodestone":
		default:
			return fmt.Errorf("invalid --source %q: must be one of 'auto', 'tomestone', 'lodestone'", source)
		}

		repo := container.Load.CharacterRepository()
		if repo == nil {
			return fmt.Errorf("character repository not initialised")
		}
		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue not initialised")
		}
		defer func() {
			if err := q.Close(); err != nil {
				container.Load.Logger().ErrorContext(cmd.Context(), "queue.close_error", slog.Any("error", err))
			}
		}()
		logger := container.Load.Logger()

		publishBatch := func() error {
			var jobs []contract.QueueJob
			if fillGaps {
				maxID, err := repo.MaxID(cmd.Context())
				if err != nil {
					return fmt.Errorf("lookup max character id for gap fill: %w", err)
				}
				if maxID == 0 {
					actualFrom, actualTo, err := computeIDSweepRange(0, 0, count, true, func() (uint32, error) { return 0, nil })
					if err != nil {
						return err
					}
					jobs = buildIDSweepJobs(actualFrom, actualTo, chunkSize, source)
				} else {
					gaps, err := repo.FindIDGaps(cmd.Context(), maxID, maxGaps)
					if err != nil {
						return fmt.Errorf("find id gaps: %w", err)
					}
					if len(gaps) == 0 {
						logger.InfoContext(cmd.Context(), "publish.id_sweep_gaps.none_found", slog.Uint64("max_id", uint64(maxID)))
						return nil
					}
					jobs = buildGapSweepJobs(gaps, chunkSize, source)
				}
			} else {
				maxIDFunc := func() (uint32, error) {
					return repo.MaxID(cmd.Context())
				}
				actualFrom, actualTo, err := computeIDSweepRange(from, to, count, auto, maxIDFunc)
				if err != nil {
					return err
				}
				jobs = buildIDSweepJobs(actualFrom, actualTo, chunkSize, source)
			}

			if len(jobs) == 0 {
				return nil
			}

			if _, err := publishAll(q, logger, cmd.Context(), jobs); err != nil {
				return err
			}
			logger.InfoContext(
				cmd.Context(), "publish.id_sweep",
				slog.Bool("fill_gaps", fillGaps),
				slog.Uint64("chunk_size", uint64(chunkSize)),
				slog.String("source", source),
				slog.Int("count", len(jobs)),
			)
			return nil
		}

		if !daemon {
			return publishBatch()
		}

		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		logger.InfoContext(
			ctx, "publish.id_sweep_daemon.started",
			slog.Duration("interval", daemonInterval),
			slog.Bool("fill_gaps", fillGaps),
			slog.String("source", source),
		)

		if err := publishBatch(); err != nil {
			logger.WarnContext(ctx, "publish.id_sweep_daemon.initial_sweep_error", slog.Any("error", err))
		}

		ticker := time.NewTicker(daemonInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.InfoContext(ctx, "publish.id_sweep_daemon.stopped")
				return nil
			case <-ticker.C:
				if err := publishBatch(); err != nil {
					logger.WarnContext(ctx, "publish.id_sweep_daemon.publish_error", slog.Any("error", err))
				}
			}
		}
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
		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue not initialised")
		}
		defer func() {
			if err := q.Close(); err != nil {
				container.Load.Logger().ErrorContext(cmd.Context(), "queue.close_error", slog.Any("error", err))
			}
		}()
		if _, err := publishAll(q, container.Load.Logger(), cmd.Context(), jobs); err != nil {
			return err
		}
		container.Load.Logger().InfoContext(
			cmd.Context(), "publish.character_census",
			slog.String("older_than", olderThan.String()),
			slog.Int("limit", limit),
			slog.Int("stale", len(stale)),
			slog.Int("count", len(jobs)),
		)
		return nil
	},
}

var publishAchievementCensusCmd = &cobra.Command{
	Use:   "achievement-census",
	Short: "Publish achievement-census jobs for characters",
	RunE: func(cmd *cobra.Command, args []string) error {
		charID, _ := cmd.Flags().GetUint32("character-id")
		limit, _ := cmd.Flags().GetInt("limit")

		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue not initialised")
		}
		defer func() {
			if err := q.Close(); err != nil {
				container.Load.Logger().ErrorContext(cmd.Context(), "queue.close_error", slog.Any("error", err))
			}
		}()

		if charID > 0 {
			job := handler.AchievementCensusJob(charID)
			if err := q.Publish(cmd.Context(), job); err != nil {
				return err
			}
			container.Load.Logger().InfoContext(
				cmd.Context(), "publish.achievement_census",
				slog.Uint64("character_id", uint64(charID)),
			)
			return nil
		}

		if limit <= 0 {
			limit = 1000
		}
		repo := container.Load.CharacterRepository()
		if repo == nil {
			return fmt.Errorf("character repository not initialised")
		}
		chars, err := repo.List(cmd.Context(), contract.CharacterFilter{}, limit, 0)
		if err != nil {
			return fmt.Errorf("list characters: %w", err)
		}

		var jobs []contract.QueueJob
		for _, c := range chars {
			jobs = append(jobs, handler.AchievementCensusJob(c.ID))
		}

		if _, err := publishAll(q, container.Load.Logger(), cmd.Context(), jobs); err != nil {
			return err
		}
		container.Load.Logger().InfoContext(
			cmd.Context(), "publish.achievement_census",
			slog.Int("limit", limit),
			slog.Int("found", len(chars)),
			slog.Int("count", len(jobs)),
		)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(publishCmd)
	publishCmd.AddCommand(publishIDSweepCmd)
	publishIDSweepCmd.Flags().Bool("auto", false, "automatically determine next ID range starting from max_id + 1")
	publishIDSweepCmd.Flags().Uint32("batch-size", 1000, "number of character IDs to sweep (alias for --count)")
	publishIDSweepCmd.Flags().Uint32("from", 0, "first character ID (default: max_id + 1)")
	publishIDSweepCmd.Flags().Uint32("to", 0, "last character ID (default: from + count - 1)")
	publishIDSweepCmd.Flags().Uint32("count", 1000, "number of character IDs to sweep when --to is omitted")
	publishIDSweepCmd.Flags().Uint32("chunk-size", 100, "IDs per id-sweep job")
	publishIDSweepCmd.Flags().String("source", "auto", "ingest source (auto, tomestone, lodestone)")
	publishIDSweepCmd.Flags().Bool("fill-gaps", false, "scan unscanned holes between 1 and MaxID")
	publishIDSweepCmd.Flags().Bool("daemon", false, "run continuous auto-sweep loop")
	publishIDSweepCmd.Flags().Duration("daemon-interval", 30*time.Second, "tick interval for daemon checks")
	publishIDSweepCmd.Flags().Int("max-gaps", 50, "max gap ranges to query per run in --fill-gaps mode")
	publishCmd.AddCommand(publishCharacterCensusCmd)
	publishCharacterCensusCmd.Flags().Duration("older-than", 720*time.Hour, "only re-census characters not seen within this duration")
	publishCharacterCensusCmd.Flags().Int("limit", 1000, "max characters to enqueue")
	publishCmd.AddCommand(publishAchievementCensusCmd)
	publishAchievementCensusCmd.Flags().Uint32("character-id", 0, "specific character ID to census")
	publishAchievementCensusCmd.Flags().Int("limit", 1000, "max characters to enqueue")
}
