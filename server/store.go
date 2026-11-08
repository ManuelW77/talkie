package main

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store kapselt die SQLite-Datenbank mit Nutzern, Einladungen, Räumen und Chat.
type Store struct {
	db *sql.DB
}

type DBUser struct {
	ID       int64
	Name     string
	PubKey   []byte
	Banned   bool
	Created  time.Time
	LastSeen time.Time
}

type DBRoom struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type DBInvite struct {
	Code     string    `json:"code"`
	UsesLeft int       `json:"uses_left"`
	Expires  time.Time `json:"expires"`
}

var (
	ErrNotFound      = errors.New("nicht gefunden")
	ErrNameTaken     = errors.New("name vergeben")
	ErrInviteInvalid = errors.New("einladung ungültig")
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	name      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	pubkey    BLOB NOT NULL UNIQUE,
	banned    INTEGER NOT NULL DEFAULT 0,
	created   INTEGER NOT NULL,
	last_seen INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS invites (
	code      TEXT PRIMARY KEY,
	created   INTEGER NOT NULL,
	expires   INTEGER NOT NULL,
	uses_left INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS rooms (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	name     TEXT NOT NULL UNIQUE COLLATE NOCASE,
	position INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS messages (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	room_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	name    TEXT NOT NULL,
	text    TEXT NOT NULL,
	ts      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS messages_room ON messages(room_id, id);
`

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: ein Schreiber, vermeidet SQLITE_BUSY
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	s := &Store{db: db}
	rooms, err := s.Rooms()
	if err != nil {
		db.Close()
		return nil, err
	}
	if len(rooms) == 0 {
		for _, n := range []string{"Lobby", "Zocken 1", "Zocken 2"} {
			if _, err := s.AddRoom(n); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func scanUser(row interface{ Scan(...any) error }) (*DBUser, error) {
	var u DBUser
	var banned int
	var created, seen int64
	if err := row.Scan(&u.ID, &u.Name, &u.PubKey, &banned, &created, &seen); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.Banned = banned != 0
	u.Created = time.Unix(created, 0)
	if seen > 0 {
		u.LastSeen = time.Unix(seen, 0)
	}
	return &u, nil
}

const userCols = "id, name, pubkey, banned, created, last_seen"

func (s *Store) UserByPubKey(pk []byte) (*DBUser, error) {
	return scanUser(s.db.QueryRow("SELECT "+userCols+" FROM users WHERE pubkey = ?", pk))
}

func (s *Store) UserByName(name string) (*DBUser, error) {
	return scanUser(s.db.QueryRow("SELECT "+userCols+" FROM users WHERE name = ?", name))
}

func (s *Store) Users() ([]DBUser, error) {
	rows, err := s.db.Query("SELECT " + userCols + " FROM users ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DBUser
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// Register verbraucht eine Einladung und legt den Nutzer an — atomar.
func (s *Store) Register(invite, name string, pk []byte) (*DBUser, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	invite = NormalizeInvite(invite)
	res, err := tx.Exec("UPDATE invites SET uses_left = uses_left - 1 WHERE code = ? AND uses_left > 0 AND expires > ?",
		invite, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrInviteInvalid
	}
	var exists int
	if err := tx.QueryRow("SELECT COUNT(*) FROM users WHERE name = ?", name).Scan(&exists); err != nil {
		return nil, err
	}
	if exists > 0 {
		return nil, ErrNameTaken
	}
	now := time.Now().Unix()
	r, err := tx.Exec("INSERT INTO users(name, pubkey, created, last_seen) VALUES(?,?,?,?)", name, pk, now, now)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec("DELETE FROM invites WHERE uses_left <= 0"); err != nil {
		return nil, err
	}
	id, _ := r.LastInsertId()
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &DBUser{ID: id, Name: name, PubKey: pk, Created: time.Unix(now, 0), LastSeen: time.Unix(now, 0)}, nil
}

func (s *Store) TouchUser(id int64) error {
	_, err := s.db.Exec("UPDATE users SET last_seen = ? WHERE id = ?", time.Now().Unix(), id)
	return err
}

func (s *Store) SetBanned(name string, banned bool) (*DBUser, error) {
	u, err := s.UserByName(name)
	if err != nil {
		return nil, err
	}
	b := 0
	if banned {
		b = 1
	}
	if _, err := s.db.Exec("UPDATE users SET banned = ? WHERE id = ?", b, u.ID); err != nil {
		return nil, err
	}
	u.Banned = banned
	return u, nil
}

func (s *Store) RenameUser(oldName, newName string) (*DBUser, error) {
	u, err := s.UserByName(oldName)
	if err != nil {
		return nil, err
	}
	if other, err := s.UserByName(newName); err == nil && other.ID != u.ID {
		return nil, ErrNameTaken
	}
	if _, err := s.db.Exec("UPDATE users SET name = ? WHERE id = ?", newName, u.ID); err != nil {
		return nil, err
	}
	u.Name = newName
	return u, nil
}

func (s *Store) DeleteUser(name string) (*DBUser, error) {
	u, err := s.UserByName(name)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec("DELETE FROM users WHERE id = ?", u.ID)
	return u, err
}

// Einladungscodes: 8 Zeichen aus einem Alphabet ohne verwechselbare Zeichen
// (kein 0/O, 1/I/L), als XXXX-XXXX. ~39 Bit, zusammen mit Rate-Limit ausreichend.
const inviteAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func NormalizeInvite(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.NewReplacer(" ", "", "-", "").Replace(code)
	if len(code) == 8 {
		code = code[:4] + "-" + code[4:]
	}
	return code
}

func newInviteCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 0, 9)
	for i, v := range b {
		if i == 4 {
			out = append(out, '-')
		}
		// 256 % 31 != 0 → minimaler Bias, für Einladungscodes unerheblich
		out = append(out, inviteAlphabet[int(v)%len(inviteAlphabet)])
	}
	return string(out), nil
}

func (s *Store) CreateInvite(uses int, valid time.Duration) (*DBInvite, error) {
	code, err := newInviteCode()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	exp := now.Add(valid)
	if _, err := s.db.Exec("INSERT INTO invites(code, created, expires, uses_left) VALUES(?,?,?,?)",
		code, now.Unix(), exp.Unix(), uses); err != nil {
		return nil, err
	}
	return &DBInvite{Code: code, UsesLeft: uses, Expires: exp}, nil
}

func (s *Store) Invites() ([]DBInvite, error) {
	if _, err := s.db.Exec("DELETE FROM invites WHERE expires <= ?", time.Now().Unix()); err != nil {
		return nil, err
	}
	rows, err := s.db.Query("SELECT code, uses_left, expires FROM invites ORDER BY expires")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DBInvite
	for rows.Next() {
		var inv DBInvite
		var exp int64
		if err := rows.Scan(&inv.Code, &inv.UsesLeft, &exp); err != nil {
			return nil, err
		}
		inv.Expires = time.Unix(exp, 0)
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *Store) DeleteInvite(code string) error {
	res, err := s.db.Exec("DELETE FROM invites WHERE code = ?", NormalizeInvite(code))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Rooms() ([]DBRoom, error) {
	rows, err := s.db.Query("SELECT id, name FROM rooms ORDER BY position, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DBRoom
	for rows.Next() {
		var r DBRoom
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) AddRoom(name string) (*DBRoom, error) {
	var maxPos int
	_ = s.db.QueryRow("SELECT COALESCE(MAX(position), 0) FROM rooms").Scan(&maxPos)
	r, err := s.db.Exec("INSERT INTO rooms(name, position) VALUES(?, ?)", name, maxPos+1)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrNameTaken
		}
		return nil, err
	}
	id, _ := r.LastInsertId()
	return &DBRoom{ID: id, Name: name}, nil
}

func (s *Store) RoomByName(name string) (*DBRoom, error) {
	var r DBRoom
	err := s.db.QueryRow("SELECT id, name FROM rooms WHERE name = ?", name).Scan(&r.ID, &r.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

func (s *Store) DeleteRoom(id int64) error {
	if _, err := s.db.Exec("DELETE FROM messages WHERE room_id = ?", id); err != nil {
		return err
	}
	_, err := s.db.Exec("DELETE FROM rooms WHERE id = ?", id)
	return err
}

type DBMessage struct {
	Room int64  `json:"room"`
	From int64  `json:"from"`
	Name string `json:"name"`
	Text string `json:"text"`
	TS   int64  `json:"ts"`
}

// AddMessage speichert eine Chatnachricht und hält pro Raum nur die letzten keep Nachrichten.
func (s *Store) AddMessage(m DBMessage, keep int) error {
	if _, err := s.db.Exec("INSERT INTO messages(room_id, user_id, name, text, ts) VALUES(?,?,?,?,?)",
		m.Room, m.From, m.Name, m.Text, m.TS); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM messages WHERE room_id = ? AND id NOT IN
		(SELECT id FROM messages WHERE room_id = ? ORDER BY id DESC LIMIT ?)`, m.Room, m.Room, keep)
	return err
}

// History liefert die letzten n Nachrichten eines Raums, älteste zuerst.
func (s *Store) History(room int64, n int) ([]DBMessage, error) {
	rows, err := s.db.Query(`SELECT room_id, user_id, name, text, ts FROM
		(SELECT * FROM messages WHERE room_id = ? ORDER BY id DESC LIMIT ?) ORDER BY id`, room, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DBMessage{}
	for rows.Next() {
		var m DBMessage
		if err := rows.Scan(&m.Room, &m.From, &m.Name, &m.Text, &m.TS); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
