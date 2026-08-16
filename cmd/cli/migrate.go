package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Runs SQLite schema migrations (rewired in Task 6)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("migrate command is being reworked")
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
