# Upgrade

Migrations run forward-only on boot (`migrate.Up()`). Never edit applied SQL. There is no production downgrade.

1. `docker compose stop`
2. Copy `./config` (cold backup)
3. Pull the new image
4. `docker compose up -d`

A dirty migration exits the process with code 1. Restore `/config` from the cold backup and retry.
