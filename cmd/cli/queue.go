package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Manage and inspect the work queue",
	Long:  "Commands to inspect queue state, sampled active/next/failed jobs, replay dead-letter messages, and purge old records.",
}

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Display rich queue state, active processing, upcoming messages, and failure traces",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue service unavailable")
		}

		eventType, _ := cmd.Flags().GetString("event-type")
		sampleLimit, _ := cmd.Flags().GetInt("sample-limit")
		if sampleLimit <= 0 {
			sampleLimit = 5
		}

		details, err := q.GetEventDetails(ctx, sampleLimit)
		if err != nil {
			return fmt.Errorf("get queue event details: %w", err)
		}

		if eventType != "" {
			var filtered []contract.QueueEventDetail
			for _, d := range details {
				if d.Type == eventType {
					filtered = append(filtered, d)
				}
			}
			details = filtered
		}

		out := cmd.OutOrStdout()
		fmt.Fprintln(out, "=== Work Queue Summary ===")
		w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "EVENT TYPE\tPENDING\tCLAIMED (ACTIVE)\tDONE\tFAILED (DLQ)\tTOTAL")
		for _, d := range details {
			fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\n",
				d.Type, d.Pending, d.Claimed, d.Done, d.Failed, d.Total)
		}
		w.Flush()
		fmt.Fprintln(out)

		for _, d := range details {
			if len(d.ActiveJobs) > 0 || len(d.NextJobs) > 0 || len(d.FailedJobs) > 0 {
				fmt.Fprintf(out, "--- Event Type: %s ---\n", d.Type)

				if len(d.ActiveJobs) > 0 {
					fmt.Fprintf(out, "  Active Processing (%d sampled):\n", len(d.ActiveJobs))
					for _, j := range d.ActiveJobs {
						claimedStr := "unknown"
						if j.ClaimedAt != nil {
							claimedStr = j.ClaimedAt.Format("15:04:05.000")
						}
						fmt.Fprintf(out, "    • ID=%d | attempts=%d/%d | claimed_at=%s | payload=%s\n",
							j.ID, j.Attempts, j.MaxAttempts, claimedStr, truncatePayload(j.Payload, 80))
					}
				}

				if len(d.NextJobs) > 0 {
					fmt.Fprintf(out, "  Upcoming Queued (%d sampled):\n", len(d.NextJobs))
					for _, j := range d.NextJobs {
						fmt.Fprintf(out, "    • ID=%d | attempts=%d | run_at=%s | payload=%s\n",
							j.ID, j.Attempts, j.RunAt.Format("15:04:05.000"), truncatePayload(j.Payload, 80))
						if j.LastError != nil && *j.LastError != "" {
							fmt.Fprintf(out, "      ↳ last_retry_error: %s\n", *j.LastError)
						}
					}
				}

				if len(d.FailedJobs) > 0 {
					fmt.Fprintf(out, "  Dead-Letter Failed (%d sampled):\n", len(d.FailedJobs))
					for _, j := range d.FailedJobs {
						failedStr := "unknown"
						if j.FailedAt != nil {
							failedStr = j.FailedAt.Format("15:04:05.000")
						}
						errStr := "none"
						if j.LastError != nil && *j.LastError != "" {
							errStr = *j.LastError
						}
						fmt.Fprintf(out, "    • ID=%d | attempts=%d/%d | failed_at=%s | payload=%s\n",
							j.ID, j.Attempts, j.MaxAttempts, failedStr, truncatePayload(j.Payload, 80))
						fmt.Fprintf(out, "      ↳ error_trace: %s\n", errStr)
					}
				}
				fmt.Fprintln(out)
			}
		}

		return nil
	},
}

var retryFailedCmd = &cobra.Command{
	Use:   "retry-failed",
	Short: "Replay failed dead-letter jobs back to pending",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue service unavailable")
		}

		eventType, _ := cmd.Flags().GetString("event-type")
		limit, _ := cmd.Flags().GetInt("limit")
		if limit <= 0 {
			limit = 100
		}

		retried, err := q.RetryFailed(ctx, eventType, limit)
		if err != nil {
			return fmt.Errorf("retry failed jobs: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Replayed %d failed jobs to pending status\n", retried)
		return nil
	},
}

var purgeCmd = &cobra.Command{
	Use:   "purge",
	Short: "Purge completed or failed jobs older than a specified duration",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		q := container.Load.Queue()
		if q == nil {
			return fmt.Errorf("queue service unavailable")
		}

		statusStr, _ := cmd.Flags().GetString("status")
		status := contract.QueueJobStatus(statusStr)
		if status != contract.QueueJobDone && status != contract.QueueJobFailed {
			return fmt.Errorf("invalid status %q (allowed: done, failed)", statusStr)
		}

		olderThanStr, _ := cmd.Flags().GetString("older-than")
		duration, err := time.ParseDuration(olderThanStr)
		if err != nil || duration < 0 {
			return fmt.Errorf("invalid --older-than duration %q (e.g. 24h, 30m)", olderThanStr)
		}

		purged, err := q.PurgeJobs(ctx, status, duration)
		if err != nil {
			return fmt.Errorf("purge jobs: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Purged %d %s jobs older than %s\n", purged, status, olderThanStr)
		return nil
	},
}

func truncatePayload(payload []byte, maxLen int) string {
	str := strings.TrimSpace(string(payload))
	if len(str) <= maxLen {
		return str
	}
	return str[:maxLen-3] + "..."
}

func init() {
	statsCmd.Flags().String("event-type", "", "filter by event type (e.g. id-sweep)")
	statsCmd.Flags().Int("sample-limit", 5, "number of sampled active/next/failed jobs to display")

	retryFailedCmd.Flags().String("event-type", "", "optional event type filter")
	retryFailedCmd.Flags().Int("limit", 100, "maximum number of failed jobs to replay")

	purgeCmd.Flags().String("status", "done", "job status to purge (done or failed)")
	purgeCmd.Flags().String("older-than", "24h", "duration threshold (e.g. 24h, 72h)")

	queueCmd.AddCommand(statsCmd)
	queueCmd.AddCommand(retryFailedCmd)
	queueCmd.AddCommand(purgeCmd)
}
