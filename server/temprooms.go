package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Temporäre Räume: von App-Nutzern erstellt, nur im Speicher des Hubs.
// Sie verschwinden samt Chat, sobald der letzte Nutzer sie verlässt.
const (
	tempRoomFirstID   = 1_000_000_000 // eigener Zähler, kollidiert nie mit Räumen aus der Datenbank
	maxTempRooms      = 20
	maxTempPerCreator = 1
	maxRoomPassword   = 64  // Zeichen
	maxJoinPassword   = 256 // Zeichen, die beim Beitreten überhaupt geprüft werden
	tempChatKeep      = 200
	passwordTries     = 5
	passwordWindow    = time.Minute
)

var (
	errNoRoom       = errors.New("kein solcher raum")
	errRoomPassword = errors.New("falsches passwort")
	errTooManyTries = errors.New("zu viele versuche")
)

type tempRoom struct {
	id      int64
	name    string
	creator int64
	salt    []byte
	hash    []byte // nil = ohne Passwort; Klartext wird nie gespeichert
	msgs    []DBMessage
}

func (r *tempRoom) locked() bool { return r.hash != nil }

func hashPassword(salt []byte, pw string) []byte {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(pw))
	return h.Sum(nil)
}

func (r *tempRoom) check(pw string) bool {
	if !r.locked() {
		return true
	}
	return subtle.ConstantTimeCompare(hashPassword(r.salt, pw), r.hash) == 1
}

func (h *Hub) tempByIDLocked(id int64) *tempRoom {
	for _, r := range h.temp {
		if r.id == id {
			return r
		}
	}
	return nil
}

// allRoomsLocked: Räume aus der Datenbank, danach die temporären in Erstellungsreihenfolge.
func (h *Hub) allRoomsLocked() []DBRoom {
	out := make([]DBRoom, 0, len(h.rooms)+len(h.temp))
	out = append(out, h.rooms...)
	for _, r := range h.temp {
		out = append(out, DBRoom{ID: r.id, Name: r.name, Temp: true, Locked: r.locked()})
	}
	return out
}

func (h *Hub) roomNameTakenLocked(name string) bool {
	for _, r := range h.rooms {
		if strings.EqualFold(r.Name, name) {
			return true
		}
	}
	for _, r := range h.temp {
		if strings.EqualFold(r.name, name) {
			return true
		}
	}
	return false
}

// dropEmptyTempLocked löscht den temporären Raum id, falls niemand mehr drin ist.
func (h *Hub) dropEmptyTempLocked(id int64) {
	idx := -1
	for i, r := range h.temp {
		if r.id == id {
			idx = i
		}
	}
	if idx < 0 {
		return
	}
	for _, o := range h.sessions {
		if o.room == id {
			return
		}
	}
	h.temp = append(h.temp[:idx:idx], h.temp[idx+1:]...)
	h.broadcastLocked(&Envelope{T: "rooms", Rooms: h.allRoomsLocked()}, nil)
}

func cleanRoomPassword(pw string) (string, bool) {
	if !utf8.ValidString(pw) || utf8.RuneCountInString(pw) > maxRoomPassword {
		return "", false
	}
	return pw, true
}

// CreateRoom legt einen temporären Raum an und setzt s sofort hinein.
func (h *Hub) CreateRoom(s *Session, name, password string) {
	fail := func(code, msg string) { s.send(&Envelope{T: "error", Code: code, Reason: msg}) }
	name, ok := cleanName(name)
	if !ok {
		fail("name_invalid", badRoomName)
		return
	}
	pw, ok := cleanRoomPassword(password)
	if !ok {
		fail("password_invalid", "Das Passwort darf höchstens 64 Zeichen lang sein.")
		return
	}
	var salt, hash []byte
	if pw != "" {
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			fail("internal", "Das hat nicht geklappt.")
			return
		}
		hash = hashPassword(salt, pw)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[s.userID] != s {
		return
	}
	if h.roomNameTakenLocked(name) {
		fail("room_exists", "Diesen Raum gibt es schon. Nimm bitte einen anderen Namen.")
		return
	}
	own := 0
	for _, r := range h.temp {
		if r.creator == s.userID {
			own++
		}
	}
	if own >= maxTempPerCreator {
		fail("room_limit", "Du hast schon einen eigenen Raum. Er verschwindet, wenn alle ihn verlassen haben.")
		return
	}
	if len(h.temp) >= maxTempRooms {
		fail("room_limit", "Es gibt gerade zu viele eigene Räume. Versuch es später nochmal.")
		return
	}
	h.nextTempID++
	r := &tempRoom{id: h.nextTempID, name: name, creator: s.userID, salt: salt, hash: hash}
	h.temp = append(h.temp, r)
	old := s.room
	s.room = r.id
	h.broadcastLocked(&Envelope{T: "rooms", Rooms: h.allRoomsLocked()}, nil)
	h.broadcastLocked(&Envelope{T: "user_update", User: ptr(s.wireLocked())}, nil)
	h.dropEmptyTempLocked(old)
	s.send(&Envelope{T: "history", Room: r.id, Messages: []DBMessage{}})
}

// tempHistory liefert den Verlauf eines temporären Raums (ok=false: kein temporärer Raum).
func (h *Hub) tempHistory(room int64) ([]DBMessage, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r := h.tempByIDLocked(room)
	if r == nil {
		return nil, false
	}
	n := len(r.msgs)
	if n > historySize {
		n = historySize
	}
	return append([]DBMessage{}, r.msgs[len(r.msgs)-n:]...), true
}

func passwordKey(userID int64) string { return "u" + strconv.FormatInt(userID, 10) }
