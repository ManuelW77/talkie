package main

import (
	"database/sql"
	"errors"
	"time"
)

// Mehrere Geräte pro Nutzer: jedes Gerät hat seinen eigenen Schlüssel. Ein neues Gerät kommt
// mit einem Geräte-Code dazu (eine Einladung, die an einen Nutzer gebunden ist).

const devicesSchema = `
CREATE TABLE IF NOT EXISTS devices (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	pubkey    BLOB NOT NULL UNIQUE,
	label     TEXT NOT NULL DEFAULT '',
	created   INTEGER NOT NULL,
	last_seen INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS devices_user ON devices(user_id);
`

// MaxDevices begrenzt die Geräte pro Nutzer.
const MaxDevices = 5

var ErrTooManyDevices = errors.New("zu viele geräte")

type DBDevice struct {
	ID       int64
	UserID   int64
	Label    string
	Created  time.Time
	LastSeen time.Time
}

// initDevices legt die Gerätetabelle an und übernimmt die Schlüssel bestehender Nutzer.
func (s *Store) initDevices() error {
	if _, err := s.db.Exec(devicesSchema); err != nil {
		return err
	}
	// Einladungen können an einen Nutzer gebunden sein (= Geräte-Code)
	var has int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('invites') WHERE name = 'user_id'").Scan(&has); err != nil {
		return err
	}
	if has == 0 {
		if _, err := s.db.Exec("ALTER TABLE invites ADD COLUMN user_id INTEGER REFERENCES users(id) ON DELETE CASCADE"); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO devices(user_id, pubkey, label, created, last_seen)
		SELECT id, pubkey, '', created, last_seen FROM users`)
	return err
}

func scanDevice(row interface{ Scan(...any) error }) (*DBDevice, error) {
	var d DBDevice
	var created, seen int64
	if err := row.Scan(&d.ID, &d.UserID, &d.Label, &created, &seen); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	d.Created = time.Unix(created, 0)
	if seen > 0 {
		d.LastSeen = time.Unix(seen, 0)
	}
	return &d, nil
}

// Devices liefert die Geräte eines Nutzers, zuletzt benutzte zuerst.
func (s *Store) Devices(userID int64) ([]DBDevice, error) {
	rows, err := s.db.Query("SELECT id, user_id, label, created, last_seen FROM devices WHERE user_id = ? ORDER BY last_seen DESC, id", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DBDevice
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// DeviceByPubKey liefert das Gerät zu einem Schlüssel.
func (s *Store) DeviceByPubKey(pk []byte) (*DBDevice, error) {
	return scanDevice(s.db.QueryRow("SELECT id, user_id, label, created, last_seen FROM devices WHERE pubkey = ?", pk))
}

// DeleteDevice entfernt ein Gerät des Nutzers. Das letzte Gerät bleibt (dafür den Nutzer löschen).
func (s *Store) DeleteDevice(userID, deviceID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM devices WHERE user_id = ?", userID).Scan(&n); err != nil {
		return err
	}
	if n <= 1 {
		return errors.New("Das letzte Gerät kann nicht entfernt werden – dafür den Nutzer entfernen.")
	}
	r, err := tx.Exec("DELETE FROM devices WHERE id = ? AND user_id = ?", deviceID, userID)
	if err != nil {
		return err
	}
	if c, _ := r.RowsAffected(); c != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}

// DevicePubKey liefert den Schlüssel eines Geräts des Nutzers.
func (s *Store) DevicePubKey(userID, deviceID int64) ([]byte, error) {
	var pk []byte
	err := s.db.QueryRow("SELECT pubkey FROM devices WHERE id = ? AND user_id = ?", deviceID, userID).Scan(&pk)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return pk, err
}

func (s *Store) TouchDevice(pk []byte) {
	_, _ = s.db.Exec("UPDATE devices SET last_seen = ? WHERE pubkey = ?", time.Now().Unix(), pk)
}

// CreateDeviceCode erzeugt einen einmaligen Code, mit dem ein weiteres Gerät dem Nutzer
// beitritt. Ältere offene Codes des Nutzers verfallen dabei.
func (s *Store) CreateDeviceCode(userID int64, valid time.Duration) (*DBInvite, error) {
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM devices WHERE user_id = ?", userID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= MaxDevices {
		return nil, ErrTooManyDevices
	}
	if _, err := s.db.Exec("DELETE FROM invites WHERE user_id = ?", userID); err != nil {
		return nil, err
	}
	code, err := newInviteCode()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	exp := now.Add(valid)
	if _, err := s.db.Exec("INSERT INTO invites(code, created, expires, uses_left, user_id) VALUES(?,?,?,1,?)",
		code, now.Unix(), exp.Unix(), userID); err != nil {
		return nil, err
	}
	return &DBInvite{Code: code, UsesLeft: 1, Expires: exp}, nil
}

// DeviceCode liefert den offenen Geräte-Code eines Nutzers (nil = keiner).
func (s *Store) DeviceCode(userID int64) *DBInvite {
	var inv DBInvite
	var exp int64
	err := s.db.QueryRow("SELECT code, uses_left, expires FROM invites WHERE user_id = ? AND expires > ? AND uses_left > 0",
		userID, time.Now().Unix()).Scan(&inv.Code, &inv.UsesLeft, &exp)
	if err != nil {
		return nil
	}
	inv.Expires = time.Unix(exp, 0)
	return &inv
}

// addDevice: Geräte-Code einlösen (in der Register-Transaktion).
func addDevice(tx *sql.Tx, userID int64, pk []byte, label string) (*DBUser, error) {
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM devices WHERE user_id = ?", userID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= MaxDevices {
		return nil, ErrTooManyDevices
	}
	now := time.Now().Unix()
	if _, err := tx.Exec("INSERT INTO devices(user_id, pubkey, label, created, last_seen) VALUES(?,?,?,?,?)",
		userID, pk, label, now, now); err != nil {
		return nil, err
	}
	return scanUser(tx.QueryRow("SELECT "+userCols+" FROM users WHERE id = ?", userID))
}
