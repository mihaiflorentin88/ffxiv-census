# Backup & Point-in-Time Recovery

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

```bash
# Using a service account file
./bin/ffxiv-census backup \
  --target gdrive \
  --gdrive-folder-id "1abc123XYZ..." \
  --service-account-file "/secrets/gdrive-service-account.json"

# Using a Base64-encoded service account key (useful in CI/CD or env files)
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
service_account_b64 = ""
gdrive_folder_id = ""
```

#### 2. Environment Variables (Viper Mapping)

Viper automatically maps uppercase environment variables with underscores to configuration keys:

| Variable | Config Mapping | Description |
|---|---|---|
| `SQLITE_PATH` | `[sqlite] path` | Source SQLite database file to snapshot |
| `BACKUP_GDRIVE_FOLDER_ID` | `[backup] gdrive_folder_id` | Default Google Drive destination folder ID |
| `BACKUP_SERVICE_ACCOUNT_B64` | `[backup] service_account_b64` | Base64-encoded Service Account JSON key |
| `GOOGLE_APPLICATION_CREDENTIALS` | External Google SDK | Path to Google Service Account credentials file |

#### 3. Precedence Order

1. **CLI Flags** (`--gdrive-folder-id`, `--service-account-b64`, `--service-account-file`, `--service-account-json`) — Highest precedence.
2. **Environment Variables** (`BACKUP_GDRIVE_FOLDER_ID`, `BACKUP_SERVICE_ACCOUNT_B64`, `SQLITE_PATH`, `GOOGLE_APPLICATION_CREDENTIALS`).
3. **Configuration File** (`config.toml` `[backup]` and `[sqlite]` blocks).

When `BACKUP_GDRIVE_FOLDER_ID` and `BACKUP_SERVICE_ACCOUNT_B64` (or their `config.toml` entries) are set, you can run a Google Drive backup without passing credentials on the CLI:

```bash
./bin/ffxiv-census backup --target gdrive
```

## Automated Cronjob Setup

To configure daily backups with automatic rotation, add the following to your crontab (`crontab -e`):

```bash
# Run local backup daily at 02:00 AM with 14-day retention (database path defaults to config.toml or SQLITE_PATH)
0 2 * * * cd /opt/ffxiv-census && SQLITE_PATH=/var/lib/ffxiv-census/census.db ./bin/ffxiv-census backup --target local --output /var/backups/census --retention-days 14 >> /var/log/census-backup.log 2>&1

# Run Google Drive backup daily at 03:00 AM using environment variables
0 3 * * * cd /opt/ffxiv-census && SQLITE_PATH=/var/lib/ffxiv-census/census.db BACKUP_GDRIVE_FOLDER_ID="1xyz..." BACKUP_SERVICE_ACCOUNT_B64="..." ./bin/ffxiv-census backup --target gdrive >> /var/log/census-backup.log 2>&1
```
