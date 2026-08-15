package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/mysql/fixtures"
)

var fixturesCmd = &cobra.Command{
	Use:   "fixtures",
	Short: "Manage MySQL seed data",
}

var fixturesGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate example SQL fixtures",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("dir")
		count, _ := cmd.Flags().GetInt("count")
		timeout, _ := cmd.Flags().GetDuration("timeout")

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		gen := container.Load.MySQLFixturesGenerator()
		if gen == nil {
			return fmt.Errorf("mysql fixture generator not initialised")
		}
		path, err := gen.Generate(ctx, dir, count)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "fixture written to %s\n", path)
		return nil
	},
}

var fixturesLoadCmd = &cobra.Command{
	Use:   "load",
	Short: "Execute SQL fixtures against the database",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("dir")
		timeout, _ := cmd.Flags().GetDuration("timeout")

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		loader := container.Load.MySQLFixturesLoader()
		if loader == nil {
			return fmt.Errorf("mysql fixture loader not initialised")
		}
		return loader.Load(ctx, dir)
	},
}

func init() {
	rootCmd.AddCommand(fixturesCmd)
	fixturesCmd.AddCommand(fixturesGenerateCmd)
	fixturesCmd.AddCommand(fixturesLoadCmd)

	for _, sub := range []*cobra.Command{fixturesGenerateCmd, fixturesLoadCmd} {
		sub.Flags().String("dir", fixtures.DefaultDirectory, "Directory where SQL fixture files live")
		sub.Flags().Duration("timeout", 15*time.Second, "Execution timeout")
	}
	fixturesGenerateCmd.Flags().Int("count", 3, "How many rows to seed into the example table")
}
