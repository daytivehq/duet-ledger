package app

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"filippo.io/age"
)

func backupFilename(name string) bool {
	return strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".zip.age")
}
func (a *App) backupConfigured() bool {
	var value string
	return a.DB.QueryRow("SELECT value FROM server_settings WHERE key='backup_password'").Scan(&value) == nil && value != ""
}
func (a *App) backupPassword() (string, error) {
	var value string
	err := a.DB.QueryRow("SELECT value FROM server_settings WHERE key='backup_password'").Scan(&value)
	if err == sql.ErrNoRows || (err == nil && value == "") {
		return "", bad("먼저 백업 비밀번호를 설정해 주세요.")
	}
	if err != nil {
		return "", err
	}
	return a.decrypt(value)
}
func (a *App) backupSettings(w http.ResponseWriter, r *http.Request, u *User) error {
	if !u.Admin {
		return forbidden()
	}
	writeJSON(w, 200, map[string]bool{"configured": a.backupConfigured()})
	return nil
}
func (a *App) setBackupPassword(w http.ResponseWriter, r *http.Request, u *User) error {
	if !u.Admin {
		return forbidden()
	}
	if !a.rateAllowed(r, "backup-security:"+u.ID, 10) {
		return &apiError{429, "rate_limit", "잠시 후 다시 시도해 주세요."}
	}
	var v struct {
		Password       string `json:"password"`
		Code           string `json:"code"`
		BackupPassword string `json:"backup_password"`
	}
	if err := decode(r, &v); err != nil {
		return err
	}
	if utf8.RuneCountInString(v.BackupPassword) < 12 || len(v.BackupPassword) > 256 || strings.TrimSpace(v.BackupPassword) == "" {
		return bad("백업 비밀번호는 12자 이상, 256바이트 이하로 입력해 주세요.")
	}
	if err := a.reauth(u, v.Password, v.Code); err != nil {
		return err
	}
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO server_settings(key,value) VALUES('backup_password',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", a.encrypt(v.BackupPassword)); err != nil {
		return err
	}
	if err = audit(tx, u, "backup_password_changed", "server", ""); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]bool{"configured": true})
	return nil
}

// The plaintext ZIP (including its key) only exists in a private staging directory.
// Only a fully authenticated encrypted archive is published to the backup list.
func (a *App) encryptedBackup(beforeRestore bool) (string, error) {
	password, err := a.backupPassword()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(a.Config.DataDir, ".duet-backup-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	name, err := a.writeBackupArchive(dir, beforeRestore, true)
	if err != nil {
		return "", err
	}
	input, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return "", err
	}
	defer input.Close()
	recipient, err := age.NewScryptRecipient(password)
	if err != nil {
		return "", err
	}
	name += ".age"
	target := filepath.Join(a.Config.DataDir, "backups", name)
	temp := target + ".partial"
	defer os.Remove(temp)
	output, err := os.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer output.Close()
	encrypted, err := age.Encrypt(output, recipient)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	if _, err = io.Copy(encrypted, io.TeeReader(input, digest)); err != nil {
		return "", err
	}
	if err = encrypted.Close(); err != nil {
		return "", err
	}
	if err = output.Sync(); err != nil {
		return "", err
	}
	if err = output.Close(); err != nil {
		return "", err
	}
	check, err := os.Open(temp)
	if err != nil {
		return "", err
	}
	defer check.Close()
	identity, err := backupIdentity(password)
	if err != nil {
		return "", err
	}
	plain, err := age.Decrypt(check, identity)
	if err != nil {
		return "", err
	}
	verified := sha256.New()
	if _, err = io.Copy(verified, plain); err != nil {
		return "", err
	}
	if !secureEqual(string(digest.Sum(nil)), string(verified.Sum(nil))) {
		return "", fmt.Errorf("encrypted backup verification failed")
	}
	if err = os.Rename(temp, target); err != nil {
		return "", err
	}
	folder, err := os.Open(filepath.Dir(target))
	if err != nil {
		return "", err
	}
	defer folder.Close()
	if err = folder.Sync(); err != nil {
		return "", err
	}
	return name, nil
}
func backupIdentity(password string) (*age.ScryptIdentity, error) {
	identity, err := age.NewScryptIdentity(password)
	if err == nil {
		identity.SetMaxWorkFactor(18)
	}
	return identity, err
}

// RestoreWithPassword also accepts legacy ZIPs with their separate key file.
// New archives embed the original key inside the encrypted ZIP, then rotate it.
func RestoreWithPassword(archive, dataDir, keyFile, password string) error {
	input, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer input.Close()
	var magic [22]byte
	n, _ := input.Read(magic[:])
	if string(magic[:n]) != "age-encryption.org/v1\n" {
		return Restore(archive, dataDir, keyFile)
	}
	if password == "" {
		return fmt.Errorf("backup password required")
	}
	if _, err = input.Seek(0, io.SeekStart); err != nil {
		return err
	}
	identity, err := backupIdentity(password)
	if err != nil {
		return err
	}
	plain, err := age.Decrypt(input, identity)
	if err != nil {
		return fmt.Errorf("invalid backup password or damaged archive")
	}
	// Use the data volume: the read-only Docker image has only a 64MB /tmp.
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	if entries, e := os.ReadDir(abs); e == nil && len(entries) > 0 {
		return fmt.Errorf("restore requires a new or empty data directory")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(filepath.Dir(abs), ".duet-backup-decrypt-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "backup.zip")
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	size, copyErr := io.Copy(output, io.LimitReader(plain, (10<<30)+1))
	closeErr := output.Close()
	if copyErr != nil || size > 10<<30 {
		return fmt.Errorf("damaged archive or restore size limit exceeded")
	}
	if closeErr != nil {
		return closeErr
	}
	target := filepath.Join(dir, "data")
	if err = restoreArchive(path, target, "", true); err != nil {
		return err
	}
	if err = rotateRestoredKey(target, password); err != nil {
		return err
	}
	// Publish only after validation and key rotation both succeed.
	return os.Rename(target, abs)
}
func rotateRestoredKey(dataDir, password string) error {
	staged, err := New(Config{DataDir: dataDir})
	if err != nil {
		return err
	}
	defer staged.Close()
	rows, err := staged.DB.Query("SELECT id,totp,totp_pending FROM users")
	if err != nil {
		return err
	}
	type secret struct{ id, current, pending string }
	var secrets []secret
	for rows.Next() {
		var s secret
		if err = rows.Scan(&s.id, &s.current, &s.pending); err != nil {
			rows.Close()
			return err
		}
		if s.current != "" {
			s.current, err = staged.decrypt(s.current)
			if err != nil {
				rows.Close()
				return err
			}
		}
		if s.pending != "" {
			s.pending, err = staged.decrypt(s.pending)
			if err != nil {
				rows.Close()
				return err
			}
		}
		secrets = append(secrets, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	staged.master = random(32)
	tx, err := staged.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, s := range secrets {
		if s.current != "" {
			s.current = staged.encrypt(s.current)
		}
		if s.pending != "" {
			s.pending = staged.encrypt(s.pending)
		}
		if _, err = tx.Exec("UPDATE users SET totp=?,totp_pending=? WHERE id=?", s.current, s.pending, s.id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO server_settings(key,value) VALUES('backup_password',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", staged.encrypt(password)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, "master.key"), staged.master, 0600)
}
