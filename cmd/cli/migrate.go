package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
)

func runMigrate(direction string) error {
	switch direction {
	case "up", "down":
	default:
		return fmt.Errorf("invalid direction %q, use up or down", direction)
	}
	driver := container.Load.SQLite()
	if driver == nil {
		return fmt.Errorf("sqlite driver not initialised (check config/sqlite)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	switch direction {
	case "up":
		return driver.MigrateUp(ctx)
	case "down":
		return driver.MigrateDown(ctx)
	}
	return nil
}

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Runs SQLite schema migrations (up applies all pending; down rolls back all)",
	RunE: func(cmd *cobra.Command, args []string) error {
		direction, _ := cmd.Flags().GetString("direction")
		return runMigrate(direction)
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
	migrateCmd.Flags().String("direction", "up", "Migration direction: up or down")
}
