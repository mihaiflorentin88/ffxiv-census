package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func runRefreshUIStatsWithService(ctx context.Context, svc *census.UIStatsService, opts contract.UIStatsRefreshOptions) error {
	if svc == nil {
		return errors.New("UI statistics service unavailable")
	}
	result, err := svc.Refresh(ctx, opts)
	if err != nil {
		return fmt.Errorf("refresh UI statistics: %w", err)
	}
	if result == nil {
		return errors.New("refresh UI statistics returned no result")
	}
	if result.Skipped {
		fmt.Println("UI statistics refresh skipped: another refresh is already running")
		return nil
	}
	fmt.Printf("UI statistics refresh complete: generated_at=%s characters=%d duration=%s payload_bytes=%d\n",
		result.Snapshot.GeneratedAt.Format(time.RFC3339), result.Snapshot.SourceCharacters,
		result.Snapshot.RefreshDuration.Round(time.Millisecond), result.PayloadBytes)
	return nil
}

func runRefreshUIStats() error {
	stats := container.Load.UIStatsService()
	censusService := container.Load.CensusService()
	if stats == nil || censusService == nil {
		return errors.New("census/UI statistics service unavailable (check PostgreSQL configuration)")
	}
	timeout := 2 * time.Hour
	if cfg := container.Load.Config().Census; cfg != nil && cfg.UIStats != nil {
		if parsed, err := time.ParseDuration(cfg.UIStats.RefreshTimeout); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return runRefreshUIStatsWithService(ctx, stats, contract.UIStatsRefreshOptions{
		ActivitySince: censusService.ActivitySince(),
		MaxLevel:      censusService.MaxLevel(),
		Timeout:       timeout,
	})
}

var refreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Refresh derived application read models",
	Args:  cobra.NoArgs,
}

var refreshUIStatsCmd = &cobra.Command{
	Use:   "ui-stats",
	Short: "Refresh the bounded UI and aggregate API statistics snapshot",
	Args:  cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		return runRefreshUIStats()
	},
}

func init() {
	rootCmd.AddCommand(refreshCmd)
	refreshCmd.AddCommand(refreshUIStatsCmd)
}
