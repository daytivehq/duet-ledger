package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEncryptedBackupWithoutSeparateKeyPreservesTOTPAndPhotos(t *testing.T) {
	a, _, c := newTestApp(t)
	account := c.account(c.personalLedger(), "wallet", "bank", "KRW", "1000")
	pid := c.create(txInput(account, "expense", "125"))
	ref, _ := uploadPurgePhoto(t, c, pid)
	photo := c.send("GET", "/receipts/"+ref.ID, nil, 200, nil)
	makeBackup(c) // Configure before enabling TOTP, which also protects settings changes.
	secret := "JBSWY3DPEHPK3PXP"
	if _, err := a.DB.Exec("UPDATE users SET totp=?,totp_pending=?,recovery='[\"preserved-hash\"]' WHERE id=?", a.encrypt(secret), a.encrypt(secret), c.user.ID); err != nil {
		t.Fatal(err)
	}
	name, err := a.encryptedBackup(false)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(a.Config.DataDir, "backups", name)
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(name, ".zip.age") || !bytes.HasPrefix(data, []byte("age-encryption.org/v1\n")) {
		t.Fatal("not an encrypted archive")
	}
	for _, sensitive := range [][]byte{a.master, []byte(secret), []byte(testBackupPassword), photo} {
		if bytes.Contains(data, sensitive) {
			t.Fatal("plaintext secret in encrypted backup")
		}
	}
	destination := filepath.Join(t.TempDir(), "restored")
	t.Run("restore does not require system temporary storage", func(t *testing.T) {
		unavailable := filepath.Join(t.TempDir(), "missing-tmp")
		t.Setenv("TMPDIR", unavailable)
		if err := RestoreWithPassword(archive, destination, "", testBackupPassword); err != nil {
			t.Fatal(err)
		}
	})
	restored, err := New(Config{DataDir: destination})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if bytes.Equal(restored.master, a.master) {
		t.Fatal("restored key was not rotated")
	}
	var current, pending, recovery string
	if err := restored.DB.QueryRow("SELECT totp,totp_pending,recovery FROM users WHERE id=?", c.user.ID).Scan(&current, &pending, &recovery); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{current, pending} {
		plain, err := restored.decrypt(value)
		if err != nil || plain != secret {
			t.Fatal("TOTP secret lost", err)
		}
		if _, ok := totpMatch(plain, totpCode(secret, time.Now().Unix()/30), 0); !ok {
			t.Fatal("original authenticator no longer works")
		}
	}
	if recovery != "[\"preserved-hash\"]" {
		t.Fatal("recovery codes lost")
	}
	var filename string
	if err := restored.DB.QueryRow("SELECT filename FROM receipts WHERE id=?", ref.ID).Scan(&filename); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "receipts", filename))
	if err != nil || !bytes.Equal(got, photo) {
		t.Fatal("receipt lost", err)
	}
	if password, err := restored.backupPassword(); err != nil || password != testBackupPassword {
		t.Fatal("automatic backup password not restored", err)
	}
	if countRows(t, restored, "sessions") != 0 {
		t.Fatal("sessions not revoked")
	}
	// A restored installation can make another independently restorable backup.
	again, err := restored.encryptedBackup(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := RestoreWithPassword(filepath.Join(destination, "backups", again), filepath.Join(t.TempDir(), "again"), "", testBackupPassword); err != nil {
		t.Fatal(err)
	}
}

func TestEncryptedRestoreRejectsWrongPasswordCorruptionAndExistingData(t *testing.T) {
	a, _, c := newTestApp(t)
	name := makeBackup(c)
	archive := filepath.Join(a.Config.DataDir, "backups", name)
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, password string
		content        []byte
	}{
		{"missing password", "", data},
		{"wrong password", "wrong password", data},
		{"truncated", testBackupPassword, data[:len(data)-1]},
		{"tampered", testBackupPassword, append(append([]byte(nil), data[:len(data)-1]...), data[len(data)-1]^1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "backup.zip.age")
			if err := os.WriteFile(file, tc.content, 0600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "restore")
			if err := RestoreWithPassword(file, target, "", tc.password); err == nil {
				t.Fatal("invalid backup accepted")
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("failed restore published data", err)
			}
		})
	}
	target := t.TempDir()
	sentinel := filepath.Join(target, "keep")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreWithPassword(archive, target, "", testBackupPassword); err == nil {
		t.Fatal("existing data overwritten")
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "untouched" {
		t.Fatal("existing data changed")
	}
	var list []BackupInfo
	c.send("GET", "/backups", nil, 200, &list)
	if len(list) != 1 || !list[0].Encrypted {
		t.Fatal("backup listing does not identify encryption")
	}
	staged, _ := filepath.Glob(filepath.Join(a.Config.DataDir, ".duet-backup-*"))
	if len(staged) != 0 {
		t.Fatal("plaintext staging leaked")
	}
}

func TestBackupPasswordSettingsRequireAdminReauthentication(t *testing.T) {
	a, _, c := newTestApp(t)
	c.send("POST", "/backups", map[string]any{}, 400, nil)
	c.send("POST", "/backups/settings", map[string]string{"password": testPassword, "backup_password": "short"}, 400, nil)
	c.send("POST", "/backups/settings", map[string]string{"password": "wrong", "backup_password": testBackupPassword}, 401, nil)
	if a.backupConfigured() {
		t.Fatal("failed authentication configured backups")
	}
	name := makeBackup(c)
	var stored string
	if err := a.DB.QueryRow("SELECT value FROM server_settings WHERE key='backup_password'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, testBackupPassword) {
		t.Fatal("backup password stored in plaintext")
	}
	account := c.account(c.personalLedger(), "share", "bank", "KRW", "0")
	_, token := c.invite("account", account, "viewer")
	viewer := c.joinUser("backupviewer", token)
	viewer.send("GET", "/backups/settings", nil, 403, nil)
	viewer.send("POST", "/backups/settings", map[string]string{"password": testPassword, "backup_password": testBackupPassword}, 403, nil)
	// Changing the password affects future archives only.
	replacement := "replacement-backup-password"
	c.send("POST", "/backups/settings", map[string]string{"password": testPassword, "backup_password": replacement}, 200, nil)
	if err := RestoreWithPassword(filepath.Join(a.Config.DataDir, "backups", name), filepath.Join(t.TempDir(), "old"), "", testBackupPassword); err != nil {
		t.Fatal("password change broke old backup", err)
	}
	secret := "JBSWY3DPEHPK3PXP"
	if _, err := a.DB.Exec("UPDATE users SET totp=? WHERE id=?", a.encrypt(secret), c.user.ID); err != nil {
		t.Fatal(err)
	}
	c.send("POST", "/backups/settings", map[string]string{"password": testPassword, "backup_password": testBackupPassword}, 401, nil)
	c.send("POST", "/backups/settings", map[string]string{"password": testPassword, "backup_password": testBackupPassword, "code": totpCode(secret, time.Now().Unix()/30)}, 200, nil)
}

func TestEncryptedWebRestoreCrossServerKeepsLocalBackupPassword(t *testing.T) {
	source, _, s := newTestApp(t)
	name := makeBackup(s)
	secret := "JBSWY3DPEHPK3PXP"
	if _, err := source.DB.Exec("UPDATE users SET totp=? WHERE id=?", source.encrypt(secret), s.user.ID); err != nil {
		t.Fatal(err)
	}
	name, err := source.encryptedBackup(false)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(filepath.Join(source.Config.DataDir, "backups", name))
	if err != nil {
		t.Fatal(err)
	}
	a, _, c := newTestApp(t)
	localPassword := "destination-backup-password"
	c.send("POST", "/backups/settings", map[string]string{"password": testPassword, "backup_password": localPassword}, 200, nil)
	master := append([]byte(nil), a.master...)
	plan := previewBackup(t, c, "", archive, nil, 200)
	c.send("POST", "/backups/restore/confirm", restoreConfirmation(plan.Token), 200, nil)
	if !bytes.Equal(master, a.master) {
		t.Fatal("web restore changed local master key")
	}
	var value string
	if err := a.DB.QueryRow("SELECT totp FROM users WHERE id=?", s.user.ID).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if plain, err := a.decrypt(value); err != nil || plain != secret {
		t.Fatal("cross-server TOTP restore failed", err)
	}
	if password, err := a.backupPassword(); err != nil || password != localPassword {
		t.Fatal("local automatic backup password changed", err)
	}
}
