package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Runs MySQL schema migrations",
	RunE: func(cmd *cobra.Command, args []string) error {
		direction, _ := cmd.Flags().GetString("direction")
		timeout, _ := cmd.Flags().GetDuration("timeout")

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		runner := container.Load.MySQLMigrations()
		if runner == nil {
			return fmt.Errorf("mysql migrations not initialised (check config/mysql)")
		}

		switch direction {
		case "up":
			return runner.Up(ctx)
		case "down":
			return runner.Down(ctx)
		default:
			return fmt.Errorf("invalid direction %q, use up or down", direction)
		}
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
	migrateCmd.Flags().String("direction", "up", "Migration direction: up or down")
	migrateCmd.Flags().Duration("timeout", time.Minute, "Execution timeout")
}
