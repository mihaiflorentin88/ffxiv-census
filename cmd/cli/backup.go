package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/backup"
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Create a consistent SQLite snapshot and store locally or upload to Google Drive",
	Long: `Performs a consistent point-in-time snapshot of the SQLite database using VACUUM INTO.

Can store the backup in a local directory or upload directly to Google Drive
using service account credentials (suitable for automated cronjobs).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, _ := cmd.Flags().GetString("target")
		outputDir, _ := cmd.Flags().GetString("output")
		gdriveFolderID, _ := cmd.Flags().GetString("gdrive-folder-id")
		saFile, _ := cmd.Flags().GetString("service-account-file")
		saB64, _ := cmd.Flags().GetString("service-account-b64")
		saJSON, _ := cmd.Flags().GetString("service-account-json")
		retentionDays, _ := cmd.Flags().GetInt("retention-days")

		cfg := &backup.Config{
			Target:             target,
			OutputDir:          outputDir,
			GDriveFolderID:     gdriveFolderID,
			ServiceAccountFile: saFile,
			ServiceAccountB64:  saB64,
			ServiceAccountJSON: saJSON,
			RetentionDays:      retentionDays,
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		driver := container.Load.SQLite()
		if driver == nil {
			return fmt.Errorf("sqlite driver not available")
		}

		svc := backup.NewService(driver, container.Load.Logger())
		loc, err := svc.PerformBackup(ctx, cfg)
		if err != nil {
			return fmt.Errorf("backup failed: %w", err)
		}

		fmt.Printf("Backup completed successfully: %s\n", loc)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(backupCmd)
	backupCmd.Flags().StringP("target", "t", "local", "backup destination target ('local' or 'gdrive')")
	backupCmd.Flags().StringP("output", "o", "./backups", "local directory to store database backups")
	backupCmd.Flags().String("gdrive-folder-id", "", "destination Google Drive folder ID")
	backupCmd.Flags().String("service-account-file", "", "path to Google Service Account JSON key file")
	backupCmd.Flags().String("service-account-b64", "", "base64-encoded Google Service Account JSON key")
	backupCmd.Flags().String("service-account-json", "", "raw Google Service Account JSON key string")
	backupCmd.Flags().Int("retention-days", 0, "delete local backups older than N days (0 = keep all)")
}
