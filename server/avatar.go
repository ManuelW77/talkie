package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // PNG-Dekoder für set_avatar
	"log"
	"net/http"
	"strconv"
	"time"
)

// Profilbilder: 128×128 JPEG, höchstens 24 KiB, in der Tabelle users (Spalten avatar/avatar_ver).
// avatar_ver ist die Bild-Version (Unix-Millisekunden), 0 = kein Bild. Clients laden ein Bild
// nur neu, wenn sich die Version ändert.

const (
	avatarSize     = 128
	maxAvatarBytes = 24 * 1024
	avatarQuality  = 80
)

var errBadAvatar = errors.New("ungültiges profilbild")

// initAvatar ergänzt alte Datenbanken um die Bildspalten.
func (s *Store) initAvatar() error {
	cols := []struct{ name, def string }{
		{"avatar", "BLOB"},
		{"avatar_ver", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, c := range cols {
		var has int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('users') WHERE name = ?", c.name).Scan(&has); err != nil {
			return err
		}
		if has == 0 {
			if _, err := s.db.Exec("ALTER TABLE users ADD COLUMN " + c.name + " " + c.def); err != nil {
				return err
			}
		}
	}
	return nil
}

// prepareAvatar prüft ein hochgeladenes Bild (base64, JPEG oder PNG, genau 128×128) und
// kodiert es neu als JPEG (entfernt dabei Metadaten wie Ort oder Kameradaten).
func prepareAvatar(b64 string) ([]byte, error) {
	if base64.StdEncoding.DecodedLen(len(b64)) > maxAvatarBytes+3 {
		return nil, errBadAvatar
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) == 0 || len(raw) > maxAvatarBytes {
		return nil, errBadAvatar
	}
	// Erst nur die Kopfdaten lesen, damit keine riesigen Bilder dekodiert werden
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png") || cfg.Width != avatarSize || cfg.Height != avatarSize {
		return nil, errBadAvatar
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errBadAvatar
	}
	// Auf Weiß setzen (PNG mit Transparenz würde sonst schwarz)
	dst := image.NewRGBA(image.Rect(0, 0, avatarSize, avatarSize))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Over)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: avatarQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// SetAvatar speichert das Bild (leer = entfernen) und liefert die neue Version (0 = kein Bild).
// Die neue Version ist immer größer als die alte.
func (s *Store) SetAvatar(userID int64, data []byte) (int64, error) {
	if len(data) == 0 {
		r, err := s.db.Exec("UPDATE users SET avatar = NULL, avatar_ver = 0 WHERE id = ?", userID)
		if err != nil {
			return 0, err
		}
		if n, _ := r.RowsAffected(); n != 1 {
			return 0, ErrNotFound
		}
		return 0, nil
	}
	ver := time.Now().UnixMilli()
	r, err := s.db.Exec("UPDATE users SET avatar = ?, avatar_ver = MAX(?, avatar_ver + 1) WHERE id = ?", data, ver, userID)
	if err != nil {
		return 0, err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return 0, ErrNotFound
	}
	return s.AvatarVersion(userID), nil
}

// AvatarVersion liefert die Bild-Version eines Nutzers (0 = kein Bild oder unbekannt).
func (s *Store) AvatarVersion(userID int64) int64 {
	var v int64
	_ = s.db.QueryRow("SELECT avatar_ver FROM users WHERE id = ?", userID).Scan(&v)
	return v
}

// Avatar liefert Version und JPEG-Daten (0/nil = kein Bild oder unbekannter Nutzer).
func (s *Store) Avatar(userID int64) (int64, []byte) {
	var v int64
	var data []byte
	err := s.db.QueryRow("SELECT avatar_ver, avatar FROM users WHERE id = ?", userID).Scan(&v, &data)
	if err != nil || v == 0 || len(data) == 0 {
		return 0, nil
	}
	return v, data
}

// AvatarVersions liefert die Bild-Versionen aller Nutzer mit Bild (für die Verwaltungsseite).
func (s *Store) AvatarVersions() map[int64]int64 {
	out := map[int64]int64{}
	rows, err := s.db.Query("SELECT id, avatar_ver FROM users WHERE avatar_ver > 0")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, v int64
		if rows.Scan(&id, &v) == nil {
			out[id] = v
		}
	}
	return out
}

// Member ist ein Eintrag der Mitgliederliste (nicht gesperrte Nutzer).
type Member struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Avatar   int64  `json:"avatar"`
	Online   bool   `json:"online"`
	LastSeen int64  `json:"last_seen"`
}

// Members liefert alle nicht gesperrten Nutzer nach Name sortiert (ohne Online-Status).
// LastSeen ist das jüngste last_seen aller Geräte (0 = nie).
func (s *Store) Members() ([]Member, error) {
	rows, err := s.db.Query(`SELECT u.id, u.name, u.avatar_ver,
		COALESCE((SELECT MAX(d.last_seen) FROM devices d WHERE d.user_id = u.id), 0)
		FROM users u WHERE u.banned = 0 ORDER BY u.name COLLATE NOCASE, u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Name, &m.Avatar, &m.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- Hub ---

// SetOwnAvatar setzt (oder entfernt bei leerem data) das Bild des Nutzers von s.
func (h *Hub) SetOwnAvatar(s *Session, data string) {
	var jpg []byte
	if data != "" {
		var err error
		if jpg, err = prepareAvatar(data); err != nil {
			s.send(&Envelope{T: "error", Code: "bad_avatar", Reason: "Das Bild passt nicht. Probier ein anderes."})
			return
		}
	}
	ver, err := h.store.SetAvatar(s.userID, jpg)
	if err != nil {
		log.Printf("avatar speichern: %v", err)
		s.send(&Envelope{T: "error", Code: "internal", Reason: "Das hat nicht geklappt."})
		return
	}
	h.AvatarChanged(s.userID, ver)
}

// AvatarChanged merkt sich die neue Bild-Version des verbundenen Nutzers und sagt es allen.
func (h *Hub) AvatarChanged(userID, ver int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.sessions[userID]; s != nil {
		s.avatar = ver
		h.broadcastLocked(&Envelope{T: "user_update", User: ptr(s.wireLocked())}, nil)
	}
}

// SendAvatar antwortet auf get_avatar (leer, wenn es kein Bild oder den Nutzer nicht gibt).
func (h *Hub) SendAvatar(s *Session, id int64) {
	ver, data := h.store.Avatar(id)
	e := &Envelope{T: "avatar", ID: id}
	if ver > 0 {
		e.Avatar = ver
		e.Data = base64.StdEncoding.EncodeToString(data)
	}
	s.send(e)
}

// SendMembers antwortet auf members.
func (h *Hub) SendMembers(s *Session) {
	ms, err := h.store.Members()
	if err != nil {
		log.Printf("mitglieder: %v", err)
		s.send(&Envelope{T: "error", Code: "internal", Reason: "Das hat nicht geklappt."})
		return
	}
	online := h.OnlineIDs()
	for i := range ms {
		ms[i].Online = online[ms[i].ID]
	}
	// Passt die Liste nicht in einen Frame, die letzten weglassen
	for len(ms) > 0 {
		if _, err := EncodeFrame(&Envelope{T: "members", Members: ms}); err == nil {
			break
		}
		ms = ms[:len(ms)-len(ms)/10-1]
	}
	s.send(&Envelope{T: "members", Members: ms})
}

// deleteAvatar entfernt das Profilbild (Admin-CLI); {name} ist der Name oder die numerische ID.
func (a *adminAPI) deleteAvatar(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("name")
	u, err := a.store.UserByName(key)
	if errors.Is(err, ErrNotFound) {
		if id, perr := strconv.ParseInt(key, 10, 64); perr == nil {
			u, err = a.store.UserByID(id)
		}
	}
	if err == nil {
		_, err = a.store.SetAvatar(u.ID, nil)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	a.hub.AvatarChanged(u.ID, 0)
	writeJSON(w, map[string]bool{"ok": true})
}
