package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Web-Konten für die Admin-Webseite. Sie sind getrennt von den Talkie-Nutzern
// (die Kids melden sich in der App per Geräteschlüssel an, ohne E-Mail).

const webSchema = `
CREATE TABLE IF NOT EXISTS accounts (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	email      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	name       TEXT NOT NULL,
	role       TEXT NOT NULL DEFAULT 'user',
	perms      INTEGER NOT NULL DEFAULT 0,
	disabled   INTEGER NOT NULL DEFAULT 0,
	talkie_id  INTEGER,
	oidc_sub   TEXT,
	created    INTEGER NOT NULL,
	last_login INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS web_sessions (
	token_hash BLOB PRIMARY KEY,
	account_id INTEGER NOT NULL,
	csrf       TEXT NOT NULL,
	created    INTEGER NOT NULL,
	expires    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS login_tokens (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	account_id INTEGER NOT NULL,
	link_hash  BLOB NOT NULL UNIQUE,
	pend_hash  BLOB NOT NULL UNIQUE,
	code_hash  BLOB NOT NULL,
	attempts   INTEGER NOT NULL DEFAULT 0,
	used       INTEGER NOT NULL DEFAULT 0,
	created    INTEGER NOT NULL,
	expires    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS audit (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	ts      INTEGER NOT NULL,
	who     TEXT NOT NULL,
	action  TEXT NOT NULL
);
`

// Rechte für Konten der Gruppe „user“. Admins dürfen immer alles.
type Perm int

const (
	PermInvite      Perm = 1 << iota // Einladungen erstellen, löschen, per Mail senden
	PermUsersEdit                    // Talkie-Nutzer umbenennen, rauswerfen, sperren
	PermUsersDelete                  // Talkie-Nutzer löschen
	PermRooms                        // Räume anlegen, umbenennen, sortieren, löschen
	PermChat                         // Chatverlauf lesen
)

var permList = []struct {
	P     Perm
	Key   string
	Label string
}{
	{PermInvite, "invite", "Kids einladen"},
	{PermUsersEdit, "users_edit", "Talkie-Nutzer bearbeiten, rauswerfen, sperren"},
	{PermUsersDelete, "users_delete", "Talkie-Nutzer entfernen"},
	{PermRooms, "rooms", "Räume verwalten"},
	{PermChat, "chat", "Chat mitlesen"},
}

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

type Account struct {
	ID        int64
	Email     string
	Name      string
	Role      string
	Perms     Perm
	Disabled  bool
	TalkieID  int64 // 0 = nicht verknüpft
	OIDCSub   string
	Created   time.Time
	LastLogin time.Time
}

func (a Account) IsAdmin() bool { return a.Role == RoleAdmin }

func (a Account) Can(p Perm) bool { return a.IsAdmin() || a.Perms&p == p }

// Has meldet, ob das Recht ausdrücklich gesetzt ist (unabhängig von der Gruppe).
func (a Account) Has(p Perm) bool { return a.Perms&p == p }

var ErrLastAdmin = errors.New("Es muss mindestens ein aktiver Admin bleiben.")

func (s *Store) initWeb() error {
	_, err := s.db.Exec(webSchema)
	return err
}

func hashToken(t string) []byte {
	h := sha256.Sum256([]byte(t))
	return h[:]
}

const accountCols = "id, email, name, role, perms, disabled, COALESCE(talkie_id, 0), COALESCE(oidc_sub, ''), created, last_login"

func scanAccount(row interface{ Scan(...any) error }) (*Account, error) {
	var a Account
	var disabled int
	var created, last int64
	if err := row.Scan(&a.ID, &a.Email, &a.Name, &a.Role, &a.Perms, &disabled, &a.TalkieID, &a.OIDCSub, &created, &last); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	a.Disabled = disabled != 0
	a.Created = time.Unix(created, 0)
	if last > 0 {
		a.LastLogin = time.Unix(last, 0)
	}
	return &a, nil
}

func (s *Store) Accounts() ([]Account, error) {
	rows, err := s.db.Query("SELECT " + accountCols + " FROM accounts ORDER BY role, name COLLATE NOCASE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (s *Store) AccountByID(id int64) (*Account, error) {
	return scanAccount(s.db.QueryRow("SELECT "+accountCols+" FROM accounts WHERE id = ?", id))
}

func (s *Store) AccountByEmail(email string) (*Account, error) {
	return scanAccount(s.db.QueryRow("SELECT "+accountCols+" FROM accounts WHERE email = ?", normEmail(email)))
}

func (s *Store) AccountBySub(sub string) (*Account, error) {
	if sub == "" {
		return nil, ErrNotFound
	}
	return scanAccount(s.db.QueryRow("SELECT "+accountCols+" FROM accounts WHERE oidc_sub = ?", sub))
}

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func (s *Store) AddAccount(a Account) (*Account, error) {
	a.Email = normEmail(a.Email)
	now := time.Now().Unix()
	r, err := s.db.Exec("INSERT INTO accounts(email, name, role, perms, disabled, talkie_id, created) VALUES(?,?,?,?,?,?,?)",
		a.Email, a.Name, a.Role, a.Perms, boolInt(a.Disabled), nullID(a.TalkieID), now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrNameTaken
		}
		return nil, err
	}
	a.ID, _ = r.LastInsertId()
	a.Created = time.Unix(now, 0)
	return &a, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// activeAdminsExcept zählt aktive Admins ohne das genannte Konto.
func activeAdminsExcept(tx *sql.Tx, id int64) (int, error) {
	var n int
	err := tx.QueryRow("SELECT COUNT(*) FROM accounts WHERE role = 'admin' AND disabled = 0 AND id != ?", id).Scan(&n)
	return n, err
}

// UpdateAccount speichert ein Konto. Der letzte aktive Admin kann weder
// herabgestuft noch deaktiviert werden.
func (s *Store) UpdateAccount(a Account) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if a.Role != RoleAdmin || a.Disabled {
		n, err := activeAdminsExcept(tx, a.ID)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrLastAdmin
		}
	}
	res, err := tx.Exec("UPDATE accounts SET email = ?, name = ?, role = ?, perms = ?, disabled = ?, talkie_id = ? WHERE id = ?",
		normEmail(a.Email), a.Name, a.Role, a.Perms, boolInt(a.Disabled), nullID(a.TalkieID), a.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrNameTaken
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if a.Disabled {
		if _, err := tx.Exec("DELETE FROM web_sessions WHERE account_id = ?", a.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteAccount(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	n, err := activeAdminsExcept(tx, id)
	if err != nil {
		return err
	}
	var role string
	var disabled int
	if err := tx.QueryRow("SELECT role, disabled FROM accounts WHERE id = ?", id).Scan(&role, &disabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if role == RoleAdmin && disabled == 0 && n == 0 {
		return ErrLastAdmin
	}
	for _, q := range []string{"DELETE FROM web_sessions WHERE account_id = ?", "DELETE FROM login_tokens WHERE account_id = ?", "DELETE FROM accounts WHERE id = ?"} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SetOIDCSub(id int64, sub string) error {
	var v any
	if sub != "" {
		v = sub
	}
	_, err := s.db.Exec("UPDATE accounts SET oidc_sub = ? WHERE id = ?", v, id)
	return err
}

func (s *Store) HasAdmin() (bool, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM accounts WHERE role = 'admin'").Scan(&n)
	return n > 0, err
}

// --- Sitzungen ---

func (s *Store) CreateSession(accountID int64, token, csrf string, ttl time.Duration) error {
	now := time.Now()
	if _, err := s.db.Exec("DELETE FROM web_sessions WHERE expires <= ?", now.Unix()); err != nil {
		return err
	}
	if _, err := s.db.Exec("UPDATE accounts SET last_login = ? WHERE id = ?", now.Unix(), accountID); err != nil {
		return err
	}
	_, err := s.db.Exec("INSERT INTO web_sessions(token_hash, account_id, csrf, created, expires) VALUES(?,?,?,?,?)",
		hashToken(token), accountID, csrf, now.Unix(), now.Add(ttl).Unix())
	return err
}

// SessionAccount liefert das Konto zu einem Sitzungs-Token (nur aktive Konten).
func (s *Store) SessionAccount(token string) (*Account, string, error) {
	var id int64
	var csrf string
	err := s.db.QueryRow("SELECT account_id, csrf FROM web_sessions WHERE token_hash = ? AND expires > ?",
		hashToken(token), time.Now().Unix()).Scan(&id, &csrf)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	a, err := s.AccountByID(id)
	if err != nil {
		return nil, "", err
	}
	if a.Disabled {
		return nil, "", ErrNotFound
	}
	return a, csrf, nil
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec("DELETE FROM web_sessions WHERE token_hash = ?", hashToken(token))
	return err
}

// --- Login per Mail: Link + 8-stelliger Code ---

const (
	loginTTL         = 15 * time.Minute
	loginMaxAttempts = 5
)

func codeHash(pend, code string) []byte { return hashToken("code\x00" + pend + "\x00" + code) }

// CreateLogin legt einen Login-Vorgang an. link steckt im Mail-Link,
// pend im Cookie des Browsers, der die Mail angefordert hat.
func (s *Store) CreateLogin(accountID int64, link, pend, code string) error {
	now := time.Now()
	if _, err := s.db.Exec("DELETE FROM login_tokens WHERE expires <= ?", now.Unix()); err != nil {
		return err
	}
	_, err := s.db.Exec("INSERT INTO login_tokens(account_id, link_hash, pend_hash, code_hash, created, expires) VALUES(?,?,?,?,?,?)",
		accountID, hashToken(link), hashToken(pend), codeHash(pend, code), now.Unix(), now.Add(loginTTL).Unix())
	return err
}

// RecentLogins zählt angeforderte Login-Mails eines Kontos im Zeitraum.
func (s *Store) RecentLogins(accountID int64, since time.Duration) (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM login_tokens WHERE account_id = ? AND created > ?",
		accountID, time.Now().Add(-since).Unix()).Scan(&n)
	return n, err
}

// consumeLogin markiert einen Vorgang als benutzt (nur einmal möglich).
func (s *Store) consumeLogin(id int64) error {
	res, err := s.db.Exec("UPDATE login_tokens SET used = 1 WHERE id = ? AND used = 0 AND expires > ?", id, time.Now().Unix())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// LoginByLink löst einen Mail-Link ein.
func (s *Store) LoginByLink(link string) (int64, error) {
	var id, acc int64
	err := s.db.QueryRow("SELECT id, account_id FROM login_tokens WHERE link_hash = ? AND used = 0 AND expires > ?",
		hashToken(link), time.Now().Unix()).Scan(&id, &acc)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return acc, s.consumeLogin(id)
}

// LoginByCode prüft den Code für den Vorgang aus dem Cookie. Jeder Versuch
// wird vor dem Vergleich atomar gezählt; nach 5 Versuchen ist der Code wertlos.
func (s *Store) LoginByCode(pend, code string) (int64, error) {
	var id, acc int64
	var want []byte
	err := s.db.QueryRow("SELECT id, account_id, code_hash FROM login_tokens WHERE pend_hash = ? AND used = 0 AND expires > ?",
		hashToken(pend), time.Now().Unix()).Scan(&id, &acc, &want)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec("UPDATE login_tokens SET attempts = attempts + 1 WHERE id = ? AND attempts < ?", id, loginMaxAttempts)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, ErrNotFound
	}
	if subtle.ConstantTimeCompare(codeHash(pend, code), want) != 1 {
		return 0, ErrNotFound
	}
	return acc, s.consumeLogin(id)
}

// --- Talkie-Nutzer per ID (für die Webseite) ---

func (s *Store) UserByID(id int64) (*DBUser, error) {
	return scanUser(s.db.QueryRow("SELECT "+userCols+" FROM users WHERE id = ?", id))
}

func (s *Store) RoomByID(id int64) (*DBRoom, error) {
	var r DBRoom
	err := s.db.QueryRow("SELECT id, name FROM rooms WHERE id = ?", id).Scan(&r.ID, &r.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

func (s *Store) RenameRoom(id int64, name string) error {
	res, err := s.db.Exec("UPDATE rooms SET name = ? WHERE id = ?", name, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrNameTaken
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MoveRoom verschiebt einen Raum um eine Position nach oben (dir < 0) oder unten.
func (s *Store) MoveRoom(id int64, dir int) error {
	rooms, err := s.Rooms()
	if err != nil {
		return err
	}
	idx := -1
	for i, r := range rooms {
		if r.ID == id {
			idx = i
		}
	}
	if idx < 0 {
		return ErrNotFound
	}
	j := idx + 1
	if dir < 0 {
		j = idx - 1
	}
	if j < 0 || j >= len(rooms) {
		return nil
	}
	rooms[idx], rooms[j] = rooms[j], rooms[idx]
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, r := range rooms {
		if _, err := tx.Exec("UPDATE rooms SET position = ? WHERE id = ?", i+1, r.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// --- Protokoll ---

type AuditEntry struct {
	TS     time.Time
	Who    string
	Action string
}

func (s *Store) Audit(who, action string) {
	_, _ = s.db.Exec("INSERT INTO audit(ts, who, action) VALUES(?,?,?)", time.Now().Unix(), who, action)
	_, _ = s.db.Exec("DELETE FROM audit WHERE id NOT IN (SELECT id FROM audit ORDER BY id DESC LIMIT 1000)")
}

func (s *Store) AuditLog(n int) ([]AuditEntry, error) {
	rows, err := s.db.Query("SELECT ts, who, action FROM audit ORDER BY id DESC LIMIT ?", n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var ts int64
		if err := rows.Scan(&ts, &e.Who, &e.Action); err != nil {
			return nil, err
		}
		e.TS = time.Unix(ts, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}
