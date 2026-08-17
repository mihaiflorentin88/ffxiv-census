package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

var tomestoneCmd = &cobra.Command{
	Use:   "tomestone",
	Short: "Tomestone.gg API commands",
}

var tomestoneCharacterCmd = &cobra.Command{
	Use:   "character <id> | <server> <name>",
	Short: "Fetch character profile from tomestone.gg",
	Long: `Fetch character profile from tomestone.gg API.

Requires a valid API token configured via 'config.toml' [tomestone.api_token]
or TOMESTONE_API_TOKEN environment variable.

Examples:
  ffxiv-census tomestone character 36795950
  ffxiv-census tomestone character 36795950 --update
  ffxiv-census tomestone character Balmung "Tataru Taru"
`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := container.Load.TomestoneClient()
		if client == nil {
			return fmt.Errorf("tomestone client not initialised")
		}

		if !client.IsConfigured() {
			fmt.Fprintln(os.Stderr, "Warning: TOMESTONE_API_TOKEN is not set. Requests to tomestone.gg may fail with 401 Unauthenticated.")
		}

		update, _ := cmd.Flags().GetBool("update")
		raw, _ := cmd.Flags().GetBool("raw")

		var char *contract.TomestoneCharacter
		var err error

		if len(args) == 1 {
			id64, parseErr := strconv.ParseUint(args[0], 10, 32)
			if parseErr != nil {
				return fmt.Errorf("invalid character ID %q: %w", args[0], parseErr)
			}
			char, err = client.FetchCharacterProfile(cmd.Context(), uint32(id64), update)
		} else {
			server := args[0]
			name := args[1]
			char, err = client.FetchCharacterProfileByName(cmd.Context(), server, name, update)
		}

		if err != nil {
			if errors.Is(err, contract.ErrTomestoneUnauthenticated) {
				return fmt.Errorf("tomestone API authentication failed: please configure a valid token in config.toml or TOMESTONE_API_TOKEN")
			}
			if errors.Is(err, contract.ErrCharacterNotFound) {
				return fmt.Errorf("character not found on tomestone.gg")
			}
			return fmt.Errorf("fetch tomestone profile: %w", err)
		}

		if raw {
			b, _ := json.Marshal(char)
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}

		b, err := json.MarshalIndent(char, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal output: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(tomestoneCmd)
	tomestoneCmd.AddCommand(tomestoneCharacterCmd)
	tomestoneCharacterCmd.Flags().Bool("update", false, "request on-demand update from Lodestone")
	tomestoneCharacterCmd.Flags().Bool("raw", false, "print compact unindented JSON")
}
