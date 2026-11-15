# Operations, updates and recovery

[Manual home](index.md) · [Installation](installation.md)

## Routine operation

Run commands in the folder containing your Compose file, or use `docker compose -f /install/path/compose.yaml`.

```bash
docker compose ps
docker compose logs --tail 100 duet-ledger
docker compose restart duet-ledger
```

The restart policy starts the container after reboots when Docker is running. `/healthz` checks application health; it is not a backup check.

## Updating

First sync or export pending operations using the cloud icon on every device. Then read the [changelog](https://github.com/daytivehq/duet-ledger/blob/main/CHANGELOG.en.md) and save an encrypted backup and its backup password. Keep the original `master.key` for any legacy ZIP backups. Check [Releases](https://github.com/daytivehq/duet-ledger/releases) to confirm that the target image has been published.

Set the version in `.env` beside your Compose file. For example, to update to 0.4.0:

```dotenv
DUET_LEDGER_IMAGE=ghcr.io/daytivehq/duet-ledger:0.4.0
```

In a Docker management interface, change its environment variable or `image` tag. A `DUET_LEDGER_IMAGE` value in `.env` or the shell overrides the Compose default. Prefer an exact version over `latest` to control when you update.

```bash
docker compose pull duet-ledger
docker compose up -d
docker compose ps
docker compose logs --tail 100 duet-ledger
```

Pulling a fixed tag does not switch versions. Publishing an image does not automatically replace a running container.

Keep the volume and environment settings. **An old image alone is not a safe rollback after schema migration.** Use a compatible app and a pre-update data copy. Never delete the production volume during updates.

## Switching from the previous image location

If your installation uses `ghcr.io/goodjinc/duet-ledger`, first confirm that the new image is published and publicly downloadable. Change `DUET_LEDGER_IMAGE` in `.env` or `image` in Compose to `ghcr.io/daytivehq/duet-ledger:0.4.0`, then run the update commands above. The owner path must also change for `latest`. **To retain your current version, first check that the exact tag exists at the new location.**

Transferring the repository does not update existing installations' image references. Keep the same Compose project name, data volume, environment variables and server URL. No data reset or backup restoration is required. **Do not run `docker compose down -v`: it deletes the data volume.**

## Updating to 0.3.0

Updating from 0.2.0 **keeps storage schema 8** and does not convert opening balances again. Existing accounts, records, sharing relationships and app passwords are preserved.

Updating directly from 0.1.1 or earlier also applies the intervening changes. Storage upgrades to schema 8 and existing opening balances become income (positive) or expenses (negative) dated at account creation. Current balances stay the same, but historical income/expense totals change. Keep a pre-update backup and review the reports.

Refresh the app after updating to load the new interface. Offline storage defaults to enabled with seven recent days; change it under **Settings → Offline records** and disable it on shared devices. Export pending work before clearing site data to troubleshoot an outdated interface. See [Offline use](offline.md).

## Moving Docker installations

On the same Docker server, stop the old app and attach the existing `duet-ledger-data` volume and environment settings to the new stack. For a different server, use backup and restoration below. **Never open the same dataset with two app instances.**

## Backup and restore

### master.key

`master.key` is an automatically generated internal server key protecting two-factor authentication (TOTP) secrets. It does not encrypt the whole transaction database. **Do not delete or replace the running server's key.** The default Docker installation stores it at `/data/master.key` in the data volume; custom installations store it in the data directory.

**New `.zip.age` backups include the recovery key inside the encrypted file. Restore with just the backup file and backup password.** Legacy `.zip` backups still require their original `master.key`. Keep the original key for as long as you keep those backups.

Download a legacy key under **Backup and restore → Key file for legacy ZIP backups → Download key file**, after verifying your administrator password and, if enabled, a verification code. If the web interface is unavailable, copy it into a private directory. Never overwrite an existing copy.

```bash
mkdir -p private-backup
chmod 700 private-backup
docker cp duet-ledger:/data/master.key ./private-backup/master.key
chmod 600 ./private-backup/master.key
```

### Make a backup

1. Open **Administration → Backup and restore**.
2. On first use, enter and confirm a **New backup password** of at least 12 characters and at most 256 bytes. Save it after verifying your current administrator password and any required verification code.
3. Keep the backup password somewhere safe, such as 1Password. If lost, backups cannot be restored on a new server.
4. Select **Create and download backup**. The verified `.zip.age` file is saved on the server and downloaded to your device.

All users' records, receipts, shared appearance and the TOTP recovery key are encrypted. For automatic backups, the server stores the backup password encrypted under its internal key. Neither manual nor automatic backups are created before setup. Password changes affect future backups only: **older backups still require their original password.** This encryption does not prevent access by someone who controls the server.

After setup, automatic backups run daily while the server is running. Cleanup retains the latest seven files, including manual and pre-restore backups. Download important copies to another device. CSV/JSON exports do not replace a server backup.

**Sync or export pending operations on every device before restoring.** Server backups do not contain device-local pending work. [Offline guide](offline.md)

### Restore through the web interface

**All users' data will be replaced with the selected backup.** Notify other users and finish other work first.

1. Configure this server's backup password first, for the pre-restore safety backup.
2. Open **Restore** and select a saved backup or attach a `.zip.age` file.
3. Enter the **Backup password** used when that file was created. For a legacy ZIP, expand **Original key for a legacy ZIP backup** and attach its original key instead. Legacy ZIPs from this server use its current key automatically.
4. Select **Check backup** and review its date and user, account, transaction and receipt counts.
5. Verify your current administrator password and any required verification code, acknowledge replacement and select **Back up current data and restore**.
6. Sign in using credentials from the backup and check balances, transactions, photos and sharing permissions.

The server authenticates the encrypted file and checks database integrity and photos first. Incorrect passwords and damaged files cannot be restored. Current data is saved in an encrypted safety backup using this server's backup password before restoration. If backup creation or database replacement fails, existing data remains intact. To undo a successful restore, select **Automatic backup before restore** and use the password in effect when it was created.

Restoration invalidates **all login sessions, pending invitations and API app passwords**. Accounts, approved sharing, TOTP secrets and recovery codes are preserved. Your existing authenticator continues to work. Web restoration retains this server's internal key and automatic-backup password, and re-encrypts imported TOTP secrets under the current key.

Web uploads support files up to **512 MB**, with up to **10 GB** of uncompressed data. Proxy limits, available disk space and timeouts may impose lower limits. Previews last 10 minutes and must be recreated after a restart. Other requests may briefly wait during backup creation, verification or restoration.

### When the web interface is unavailable or the backup is too large

The `restore` command restores into a new directory and refuses to overwrite existing data. For new backups, use `--password-file` to read a password file, or `--password-file -` to read standard input. Never put passwords directly in command arguments or shell history. One trailing line ending is removed.

### Test a restore into a separate Docker volume

Place the backup and `password.txt` in `private-backup`. Restrict the password file to its owner and manage it securely after restoration. Replace `BACKUP.zip.age` and `IMAGE` with the actual filename and a compatible app image. Keep the production volume untouched.

```bash
docker volume create duet-ledger-restore-test
docker run --rm --user 0 \
  -v duet-ledger-restore-test:/restore \
  -v "$PWD/private-backup:/backup:ro,Z" \
  --entrypoint sh IMAGE -c \
  '/duet-ledger restore --archive /backup/BACKUP.zip.age --password-file /backup/password.txt --data-dir /restore/restored && chown -R 10001:10001 /restore/restored'
```

Command-line restoration of a new backup generates a fresh internal key and re-encrypts TOTP secrets. Future automatic backups use the password supplied for restoration. For a legacy ZIP use `--archive /backup/BACKUP.zip --key-file /backup/master.key`, then configure a backup password in the web interface. `:Z` supports SELinux environments; use a dedicated folder containing only restore copies.

Test on a separate port bound to the server itself:

```bash
docker run -d --name duet-ledger-restore-check \
  -p 127.0.0.1:18089:8080 \
  -e DUET_LEDGER_DATA_DIR=/data/restored \
  -v duet-ledger-restore-test:/data IMAGE
```

Open `http://localhost:18089`, sign in again and verify transaction counts, balances, photos and sharing. Restoration invalidates old sessions, pending invitations and app passwords. Existing accounts and sharing relationships remain, so review them before external exposure.

For a production switch, stop the test container, set the original Compose data-volume `name` to `duet-ledger-restore-test`, and set `DUET_LEDGER_DATA_DIR` to `/data/restored`. Recreate the service. **Never let two servers open the same dataset.** Preserve the previous volume until verification is complete.

### Recover a sign-in password

Use only when you control the server and cannot sign in. Identify the username and stop the app. The new password needs at least eight characters.

```bash
docker compose stop duet-ledger
read -rsp 'New password: ' duet_new_password
printf '\n'
printf '%s' "$duet_new_password" | docker compose run --rm --no-deps -T \
  duet-ledger reset-password --username ACTUAL_USERNAME
unset duet_new_password
docker compose up -d
```

If the authenticator and recovery codes are both lost, add `--disable-totp` to `reset-password`. **This revokes all sessions and app passwords for that user.** Set up two-factor authentication and new integration keys after recovery.
