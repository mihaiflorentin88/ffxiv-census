# Backup & Point-in-Time Recovery

> **Note:** PostgreSQL database backups are now managed by the standalone [pgres-chart](https://github.com/mihaiflorentin88/pgres-chart). See [External PostgreSQL](external-postgres.md) for details. The backup functionality described below applies to the legacy SQLite-based deployment only.

`ffxiv-census` provides an automated backup subsystem (`ffxiv-census backup`) built for SQLite reliability and cloud synchronization.

## Architecture

1. **Consistent Point-in-Time Snapshotting**:
   Backups utilize SQLite's native `VACUUM INTO '<destination>'` command to generate an immutable, clean, and compacted copy of the database without locking readers or requiring a process restart.
2. **Local and Cloud Destinations**:
   - **Local (`--target local`)**: Stores dated SQLite backups in a local directory (`./backups`) with automated retention cleanup (`--retention-days`).
   - **Google Drive (`--target gdrive`)**: Uploads the snapshot directly into a designated Google Drive folder via Service Account authentication.

## CLI Usage

### Local Backups

```bash
# Create a local backup in ./backups
./bin/ffxiv-census backup --target local --output ./backups

# Create a local backup and delete backups older than 7 days
./bin/ffxiv-census backup --target local --output /var/backups/census --retention-days 7
```

### Google Drive Backups

Google Drive backups support two authentication methods:

1. **OAuth 2.0 (Recommended for Personal `@gmail.com` accounts)**:
   Personal Google accounts require OAuth 2.0 with a persistent Refresh Token.
2. **Service Accounts (Google Workspace / Shared Drives)**:
   Enterprise/Workspace domains can use Service Account keys.

#### OAuth 2.0 Setup (Personal Accounts)

1. Download your OAuth 2.0 Desktop Client JSON file from Google Cloud Console (e.g. `client_secret_*.json`).
2. Run the one-time interactive authorization command:
   ```bash
   ./bin/ffxiv-census backup auth --client-secret-file client_secret_*.json
   ```
3. Open the provided URL in your browser, authorize the app, and allow Google to redirect to `http://localhost:8085`.
4. The command will automatically exchange the code for a `refresh_token` and save `BACKUP_OAUTH_CLIENT_ID`, `BACKUP_OAUTH_CLIENT_SECRET`, and `BACKUP_OAUTH_REFRESH_TOKEN` to your `.env` file.
5. Run automated backups headless:
   ```bash
   ./bin/ffxiv-census backup --target gdrive
   ```

#### Service Account Setup (Workspace Accounts)

```bash
# Using a service account file
./bin/ffxiv-census backup \
  --target gdrive \
  --gdrive-folder-id "1abc123XYZ..." \
  --service-account-file "/secrets/gdrive-service-account.json"

# Using a Base64-encoded service account key
./bin/ffxiv-census backup \
  --target gdrive \
  --gdrive-folder-id "1abc123XYZ..." \
  --service-account-b64 "$(base64 -w0 /secrets/gdrive-service-account.json)"
```

### Configuration & Environment Variables

Backup parameters can be defined in `config.toml`, overridden by environment variables via Viper, or passed directly via CLI flags.

#### 1. `config.toml` Settings

```toml
[sqlite]
path = "data/ffxiv-census.db"

[backup]
gdrive_folder_id = ""
oauth_client_id = ""
oauth_client_secret = ""
oauth_refresh_token = ""
service_account_b64 = ""
```

#### 2. Environment Variables (Viper Mapping)

Viper automatically maps uppercase environment variables with underscores to configuration keys:

| Variable | Config Mapping | Description |
|---|---|---|
| `SQLITE_PATH` | `[sqlite] path` | Source SQLite database file to snapshot |
| `BACKUP_GDRIVE_FOLDER_ID` | `[backup] gdrive_folder_id` | Destination Google Drive folder ID |
| `BACKUP_OAUTH_CLIENT_ID` | `[backup] oauth_client_id` | Google OAuth2 Client ID |
| `BACKUP_OAUTH_CLIENT_SECRET` | `[backup] oauth_client_secret` | Google OAuth2 Client Secret |
| `BACKUP_OAUTH_REFRESH_TOKEN` | `[backup] oauth_refresh_token` | Google OAuth2 persistent Refresh Token |
| `BACKUP_SERVICE_ACCOUNT_B64` | `[backup] service_account_b64` | Base64-encoded Service Account JSON key |
| `GOOGLE_APPLICATION_CREDENTIALS` | External Google SDK | Path to Google Service Account credentials file |

#### 3. Precedence Order

1. **CLI Flags** (`--oauth-client-id`, `--oauth-client-secret`, `--oauth-refresh-token`, `--service-account-file`, etc.) — Highest precedence.
2. **Environment Variables** (`BACKUP_OAUTH_REFRESH_TOKEN`, `BACKUP_GDRIVE_FOLDER_ID`, etc.).
3. **Configuration File** (`config.toml` `[backup]` and `[sqlite]` blocks).
## Automated Cronjob Setup

To configure daily backups with automatic rotation, add the following to your crontab (`crontab -e`):

```bash
# Run local backup daily at 02:00 AM with 14-day retention (database path defaults to config.toml or SQLITE_PATH)
0 2 * * * cd /opt/ffxiv-census && SQLITE_PATH=/var/lib/ffxiv-census/census.db ./bin/ffxiv-census backup --target local --output /var/backups/census --retention-days 14 >> /var/log/census-backup.log 2>&1

# Run Google Drive backup daily at 03:00 AM using environment variables
0 3 * * * cd /opt/ffxiv-census && SQLITE_PATH=/var/lib/ffxiv-census/census.db BACKUP_GDRIVE_FOLDER_ID="1xyz..." BACKUP_SERVICE_ACCOUNT_B64="..." ./bin/ffxiv-census backup --target gdrive >> /var/log/census-backup.log 2>&1
```
