# Backups and restore

ViewDock backs up application metadata: accounts, roles, libraries and scanned media records, watch history, shares, node registrations, runtime configuration and audit records. Media files, artwork caches and transcode output are not included; back those up with your normal file or object-storage tooling (for MinIO, `mc mirror` or bucket replication).

## Format

On SQLite a backup is a consistent online snapshot made with `VACUUM INTO` and checked with `PRAGMA quick_check`. On PostgreSQL it is a logical export: one JSON Lines file per application table, all read inside a single repeatable-read, read-only transaction and streamed to disk row by row. Every backup has a `manifest.json` with the ViewDock version, schema migration version, creation time, master key fingerprint and the size and SHA-256 of every file. The manifest is written last, so a backup without one is incomplete and is removed by retention after six hours.

## Secrets

Encrypted settings and node credentials are copied as stored ciphertext and are never decrypted into a backup. Back up the master key separately (`VD_MASTER_KEY`, or `master.key` in the config directory). A restore without the original key succeeds, but those secrets must be re-entered.

## Destinations and schedule

`local` writes to `<VD_CONFIG_DIR>/backups/<id>/`. `s3` writes to `<bucket>/<prefix>/<id>/` on any S3-compatible service such as MinIO. Retention keeps the newest N complete backups at the destination, whatever created them. Put local backups on a different disk or copy them off the host.

Schedule, retention and destination are runtime settings under **Admin → Settings → Backups** (`backup.schedule_hours`, default 24, 0 turns scheduled backups off; `backup.retention`, default 7; `backup.destination`; `backup.s3_*`). Each is seeded from the matching `VD_BACKUP_*` variable until an administrator saves a value; the S3 settings fall back to the `VD_STORAGE_*` variables. The command line reads the same variables, because a restore into an empty database cannot rely on stored settings.

## Admin UI

**Admin → Backups** lists backups and lets you back up now, check the destination, download a backup as `<id>.tar.gz`, verify it (checksums, schema compatibility, master key match and whether the current database is empty) and delete it. Every action is audited.

## Restore drill

Rehearse this on a spare host or container at least once per release.

1. Verify the backup: in **Admin → Backups** choose Verify, or run `viewdock backup verify <id>`.
2. Stop ViewDock. For a new host, install the same ViewDock version that created the backup (the manifest lists it) and set the same database settings and master key.
3. Make the backup available: either extract the downloaded archive with `tar -xzf <id>.tar.gz -C <VD_CONFIG_DIR>/backups`, or point the command line at object storage with `--source s3` and the `VD_BACKUP_S3_*` variables.
4. Run `viewdock backup restore <id>` (in Docker, with the service stopped: `docker compose run --rm viewdock viewdock backup restore <id>`). It re-verifies every checksum first.
    - SQLite snapshot into SQLite: the database file is replaced. An existing database is checkpointed and kept as `viewdock.db.pre-restore-<time>`. A snapshot from an older release is then upgraded by the embedded migrations.
    - Any other combination (PostgreSQL, a snapshot into PostgreSQL, or a `--logical` export into SQLite): the target is migrated, then all rows are copied in one transaction. The target schema version must equal the backup's; restore with the version that made the backup, then upgrade.
    - A database that already has user accounts is refused unless `--force` is given. With `--force`, a `pre-restore` backup of the current data is written to `<VD_CONFIG_DIR>/backups` first (rows mode), or the old file is kept (file mode).
5. Start ViewDock, sign in and check libraries, users and settings. The audit log records `backup.restore`.

Other commands: `viewdock backup list` and `viewdock backup create [--logical]`, each accepting `--source local|s3` and `--dir <path>`.

## Rollback

For file restores, stop the server and rename `viewdock.db.pre-restore-<time>` back to `viewdock.db`. For rows restores, restore the `pre-restore` backup the command created.

## Moving between SQLite and PostgreSQL

Take a backup on the old provider, point `VD_DATABASE_DRIVER` and `VD_DATABASE_URL` at the new one, and run `viewdock backup restore <id>` with the same ViewDock version.
