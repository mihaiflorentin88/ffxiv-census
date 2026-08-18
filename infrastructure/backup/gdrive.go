package backup

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Config holds settings for a database backup operation.
type Config struct {
	Target             string // "local" or "gdrive"
	OutputDir          string // Local directory to store backups (for target=local)
	GDriveFolderID     string // Google Drive destination folder ID
	ServiceAccountFile string // Path to Google service account JSON file
	ServiceAccountB64  string // Base64 encoded Google service account JSON
	ServiceAccountJSON string // Raw Google service account JSON string
	RetentionDays      int    // Retain backups for N days (0 = unlimited)
}

// Service manages SQLite point-in-time snapshots and uploads.
type Service struct {
	driver contract.SQLiteDriver
	logger contract.Logger
}

// NewService constructs a new backup Service.
func NewService(driver contract.SQLiteDriver, logger contract.Logger) *Service {
	return &Service{
		driver: driver,
		logger: logger,
	}
}

// CreateSnapshot performs a consistent VACUUM INTO '<destPath>' snapshot of the SQLite database.
func (s *Service) CreateSnapshot(ctx context.Context, destPath string) error {
	if s.driver == nil {
		return errors.New("sqlite driver is nil")
	}
	db, err := s.driver.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire sqlite db: %w", err)
	}

	// Clean any previous file at destPath
	_ = os.Remove(destPath)

	// Ensure destination directory exists
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}

	query := fmt.Sprintf("VACUUM INTO '%s'", strings.ReplaceAll(destPath, "'", "''"))
	if _, err := db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}
	return nil
}

// UploadToGDrive uploads a backup file to Google Drive using provided credentials.
func (s *Service) UploadToGDrive(ctx context.Context, filePath string, cfg *Config) (*drive.File, error) {
	opts, err := resolveGDriveOptions(cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve gdrive credentials: %w", err)
	}

	srv, err := drive.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("initialize drive service: %w", err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open backup file: %w", err)
	}
	defer f.Close()

	fileName := filepath.Base(filePath)
	driveFile := &drive.File{
		Name: fileName,
	}

	folderID := cfg.GDriveFolderID
	if folderID != "" {
		driveFile.Parents = []string{folderID}
	}

	res, err := srv.Files.Create(driveFile).Media(f).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("upload to drive: %w", err)
	}
	return res, nil
}

// PerformBackup executes a backup according to the config.
func (s *Service) PerformBackup(ctx context.Context, cfg *Config) (string, error) {
	if cfg == nil {
		return "", errors.New("backup config is nil")
	}

	timestamp := time.Now().UTC().Format("20060102_150405")
	fileName := fmt.Sprintf("ffxiv_census_backup_%s.db", timestamp)

	if cfg.Target == "" || strings.EqualFold(cfg.Target, "local") {
		outputDir := cfg.OutputDir
		if outputDir == "" {
			outputDir = "./backups"
		}
		targetPath := filepath.Join(outputDir, fileName)
		if err := s.CreateSnapshot(ctx, targetPath); err != nil {
			return "", err
		}

		if cfg.RetentionDays > 0 {
			s.cleanOldBackups(outputDir, cfg.RetentionDays)
		}
		return targetPath, nil
	}

	if strings.EqualFold(cfg.Target, "gdrive") {
		tempDir, err := os.MkdirTemp("", "census-backup-*")
		if err != nil {
			return "", fmt.Errorf("create temp backup dir: %w", err)
		}
		defer os.RemoveAll(tempDir)

		tempFile := filepath.Join(tempDir, fileName)
		if err := s.CreateSnapshot(ctx, tempFile); err != nil {
			return "", err
		}

		res, err := s.UploadToGDrive(ctx, tempFile, cfg)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("gdrive://%s (id: %s)", res.Name, res.Id), nil
	}

	return "", fmt.Errorf("unknown backup target %q (must be 'local' or 'gdrive')", cfg.Target)
}

func (s *Service) cleanOldBackups(dir string, days int) {
	if days <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "ffxiv_census_backup_") || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

// resolveGDriveOptions extracts google credentials from config or env.
func resolveGDriveOptions(cfg *Config) ([]option.ClientOption, error) {
	// 1. Explicit ServiceAccountFile flag or config
	if cfg.ServiceAccountFile != "" {
		return []option.ClientOption{
			option.WithCredentialsFile(cfg.ServiceAccountFile),
			option.WithScopes(drive.DriveFileScope),
		}, nil
	}
	// 2. Base64-encoded credentials (from flag or config)
	if cfg.ServiceAccountB64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(cfg.ServiceAccountB64)
		if err != nil {
			return nil, fmt.Errorf("decode base64 service account: %w", err)
		}
		return []option.ClientOption{
			option.WithCredentialsJSON(decoded),
			option.WithScopes(drive.DriveFileScope),
		}, nil
	}

	// 3. Raw JSON credentials string (from flag or config)
	if cfg.ServiceAccountJSON != "" {
		return []option.ClientOption{
			option.WithCredentialsJSON([]byte(cfg.ServiceAccountJSON)),
			option.WithScopes(drive.DriveFileScope),
		}, nil
	}

	// 4. Default application credentials (GOOGLE_APPLICATION_CREDENTIALS)
	if path := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); path != "" {
		return []option.ClientOption{
			option.WithCredentialsFile(path),
			option.WithScopes(drive.DriveFileScope),
		}, nil
	}

	return nil, errors.New("no Google Drive credentials found. Provide --service-account-file, --service-account-b64, or set GOOGLE_APPLICATION_CREDENTIALS")
}
