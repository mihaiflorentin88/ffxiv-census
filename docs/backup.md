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

### Environment Variables

| Variable | Description |
|---|---|
| `GDRIVE_FOLDER_ID` | Default Google Drive destination folder ID |
| `GDRIVE_SERVICE_ACCOUNT_B64` | Base64-encoded Service Account JSON key |
| `GDRIVE_SERVICE_ACCOUNT_JSON` | Raw Service Account JSON key string |
| `GOOGLE_APPLICATION_CREDENTIALS` | Path to Google Service Account JSON file |

## Automated Cronjob Setup

To configure daily backups with automatic rotation, add the following to your crontab (`crontab -e`):

```bash
# Run local backup daily at 02:00 AM with 14-day retention
0 2 * * * cd /opt/ffxiv-census && SQLITE_PATH=/var/lib/ffxiv-census/census.db ./bin/ffxiv-census backup --target local --output /var/backups/census --retention-days 14 >> /var/log/census-backup.log 2>&1

# Run Google Drive backup daily at 03:00 AM
0 3 * * * cd /opt/ffxiv-census && SQLITE_PATH=/var/lib/ffxiv-census/census.db GDRIVE_FOLDER_ID="1xyz..." GDRIVE_SERVICE_ACCOUNT_B64="..." ./bin/ffxiv-census backup --target gdrive >> /var/log/census-backup.log 2>&1
```
