package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/backup"
)

var backupCmd = newBackupCmd()

func newBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Create a consistent SQLite snapshot and store locally or upload to Google Drive",
		Long: `Performs a consistent point-in-time snapshot of the SQLite database using VACUUM INTO.

Can store the backup in a local directory or upload directly to Google Drive
using service account credentials (suitable for automated cronjobs).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var sysCfg *config.Config
			if container.Load != nil {
				sysCfg = container.Load.Config()
			}
			cfg := buildBackupConfig(cmd, sysCfg)

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

	cmd.Flags().StringP("target", "t", "local", "backup destination target ('local' or 'gdrive')")
	cmd.Flags().StringP("output", "o", "./backups", "local directory to store database backups")
	cmd.Flags().String("gdrive-folder-id", "", "destination Google Drive folder ID")
	cmd.Flags().String("service-account-file", "", "path to Google Service Account JSON key file")
	cmd.Flags().String("service-account-b64", "", "base64-encoded Google Service Account JSON key")
	cmd.Flags().String("service-account-json", "", "raw Google Service Account JSON key string")
	cmd.Flags().String("oauth-client-id", "", "Google OAuth2 Client ID")
	cmd.Flags().String("oauth-client-secret", "", "Google OAuth2 Client Secret")
	cmd.Flags().String("oauth-refresh-token", "", "Google OAuth2 Refresh Token")
	cmd.Flags().Int("retention-days", 0, "delete local backups older than N days (0 = keep all)")

	cmd.AddCommand(newBackupAuthCmd())

	return cmd
}

func buildBackupConfig(cmd *cobra.Command, sysCfg *config.Config) *backup.Config {
	target, _ := cmd.Flags().GetString("target")
	outputDir, _ := cmd.Flags().GetString("output")
	gdriveFolderID, _ := cmd.Flags().GetString("gdrive-folder-id")
	saFile, _ := cmd.Flags().GetString("service-account-file")
	saB64, _ := cmd.Flags().GetString("service-account-b64")
	saJSON, _ := cmd.Flags().GetString("service-account-json")
	oauthClientID, _ := cmd.Flags().GetString("oauth-client-id")
	oauthClientSecret, _ := cmd.Flags().GetString("oauth-client-secret")
	oauthRefreshToken, _ := cmd.Flags().GetString("oauth-refresh-token")
	retentionDays, _ := cmd.Flags().GetInt("retention-days")

	if sysCfg != nil && sysCfg.Backup != nil {
		if gdriveFolderID == "" {
			gdriveFolderID = sysCfg.Backup.GDriveFolderID
		}
		if saB64 == "" {
			saB64 = sysCfg.Backup.ServiceAccountB64
		}
		if oauthClientID == "" {
			oauthClientID = sysCfg.Backup.OAuthClientID
		}
		if oauthClientSecret == "" {
			oauthClientSecret = sysCfg.Backup.OAuthClientSecret
		}
		if oauthRefreshToken == "" {
			oauthRefreshToken = sysCfg.Backup.OAuthRefreshToken
		}
	}

	return &backup.Config{
		Target:             target,
		OutputDir:          outputDir,
		GDriveFolderID:     gdriveFolderID,
		ServiceAccountFile: saFile,
		ServiceAccountB64:  saB64,
		ServiceAccountJSON: saJSON,
		OAuthClientID:      oauthClientID,
		OAuthClientSecret:  oauthClientSecret,
		OAuthRefreshToken:  oauthRefreshToken,
		RetentionDays:      retentionDays,
	}
}

type clientSecretJSON struct {
	Installed *struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	} `json:"installed"`
	Web *struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	} `json:"web"`
}

func newBackupAuthCmd() *cobra.Command {
	var secretFile string
	var clientID string
	var clientSecret string
	var port int

	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Perform one-time Google Drive OAuth 2.0 authorization to generate a Refresh Token",
		Long: `Starts a temporary local web server and guides you through authorizing ffxiv-census to access Google Drive.
Automatically exchanges the authorization code for a Refresh Token and updates .env.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if secretFile != "" {
				data, err := os.ReadFile(secretFile)
				if err != nil {
					return fmt.Errorf("read client secret file: %w", err)
				}
				var parsed clientSecretJSON
				if err := json.Unmarshal(data, &parsed); err != nil {
					return fmt.Errorf("parse client secret file: %w", err)
				}
				if parsed.Installed != nil {
					clientID = parsed.Installed.ClientID
					clientSecret = parsed.Installed.ClientSecret
				} else if parsed.Web != nil {
					clientID = parsed.Web.ClientID
					clientSecret = parsed.Web.ClientSecret
				}
			}

			if clientID == "" || clientSecret == "" {
				var sysCfg *config.Config
				if container.Load != nil {
					sysCfg = container.Load.Config()
				}
				if sysCfg != nil && sysCfg.Backup != nil {
					if clientID == "" {
						clientID = sysCfg.Backup.OAuthClientID
					}
					if clientSecret == "" {
						clientSecret = sysCfg.Backup.OAuthClientSecret
					}
				}
			}

			if clientID == "" || clientSecret == "" {
				return errors.New("client ID and client secret are required. Provide --client-secret-file or set BACKUP_OAUTH_CLIENT_ID and BACKUP_OAUTH_CLIENT_SECRET")
			}

			listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				return fmt.Errorf("start local auth listener on port %d: %w", port, err)
			}
			defer listener.Close()

			actualPort := listener.Addr().(*net.TCPAddr).Port
			redirectURI := fmt.Sprintf("http://localhost:%d", actualPort)

			oauthCfg := &oauth2.Config{
				ClientID:     clientID,
				ClientSecret: clientSecret,
				Endpoint:     google.Endpoint,
				RedirectURL:  redirectURI,
				Scopes:       []string{drive.DriveFileScope},
			}

			authURL := oauthCfg.AuthCodeURL("state-token", oauth2.AccessTypeOffline, oauth2.ApprovalForce)

			fmt.Println("\n========================================================")
			fmt.Println("  Google Drive OAuth 2.0 Authorization")
			fmt.Println("========================================================")
			fmt.Println("1. Open the following URL in your browser:")
			fmt.Println()
			fmt.Println("   " + authURL)
			fmt.Println()
			fmt.Printf("2. Authorize the application and wait for the redirect to %s\n", redirectURI)
			fmt.Println("========================================================")

			codeChan := make(chan string, 1)
			errChan := make(chan error, 1)

			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				code := r.URL.Query().Get("code")
				if code == "" {
					http.Error(w, "Missing authorization code in query", http.StatusBadRequest)
					errChan <- errors.New("authorization failed: missing code parameter")
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprintln(w, "<h1>Authorization Successful!</h1><p>You can close this tab and return to the terminal.</p>")
				codeChan <- code
			})

			server := &http.Server{Handler: mux}
			go func() {
				if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
					errChan <- err
				}
			}()

			var code string
			select {
			case code = <-codeChan:
			case err := <-errChan:
				return fmt.Errorf("authorization callback error: %w", err)
			case <-time.After(5 * time.Minute):
				return errors.New("timed out waiting for authorization code (5 minutes)")
			}

			_ = server.Shutdown(context.Background())

			token, err := oauthCfg.Exchange(context.Background(), code)
			if err != nil {
				return fmt.Errorf("exchange authorization code for token: %w", err)
			}

			if token.RefreshToken == "" {
				return errors.New("no refresh token returned by Google (app was likely previously authorized; revoke access or use approval_prompt=force)")
			}

			fmt.Printf("\nSuccessfully obtained OAuth Refresh Token!\n")
			fmt.Printf("Refresh Token: %s\n\n", token.RefreshToken)

			// Update or append to .env
			updateEnvFile(clientID, clientSecret, token.RefreshToken)

			fmt.Println("Updated .env with BACKUP_OAUTH_CLIENT_ID, BACKUP_OAUTH_CLIENT_SECRET, and BACKUP_OAUTH_REFRESH_TOKEN.")
			return nil
		},
	}

	cmd.Flags().StringVar(&secretFile, "client-secret-file", "", "path to client_secret_*.json downloaded from Google Cloud Console")
	cmd.Flags().StringVar(&clientID, "client-id", "", "Google OAuth2 Client ID")
	cmd.Flags().StringVar(&clientSecret, "client-secret", "", "Google OAuth2 Client Secret")
	cmd.Flags().IntVar(&port, "port", 8085, "local port for OAuth redirect callback")

	return cmd
}

func updateEnvFile(clientID, clientSecret, refreshToken string) {
	content, err := os.ReadFile(".env")
	lines := []string{}
	if err == nil {
		for _, line := range strings.Split(string(content), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "BACKUP_OAUTH_CLIENT_ID=") ||
				strings.HasPrefix(trimmed, "BACKUP_OAUTH_CLIENT_SECRET=") ||
				strings.HasPrefix(trimmed, "BACKUP_OAUTH_REFRESH_TOKEN=") {
				continue
			}
			lines = append(lines, line)
		}
	}

	lines = append(lines,
		fmt.Sprintf("BACKUP_OAUTH_CLIENT_ID=%s", clientID),
		fmt.Sprintf("BACKUP_OAUTH_CLIENT_SECRET=%s", clientSecret),
		fmt.Sprintf("BACKUP_OAUTH_REFRESH_TOKEN=%s", refreshToken),
	)

	_ = os.WriteFile(".env", []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func init() {
	rootCmd.AddCommand(backupCmd)
}
