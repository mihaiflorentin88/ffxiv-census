package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "ffxiv-census",
	Short: "ffxiv-census command line interface",
	Long:  "ffxiv-census exposes operational commands for the service (serve, migrate, etc.).",
}

func Execute() error {
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.Version = fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date)
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(httpCmd)
}
