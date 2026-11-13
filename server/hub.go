package main

import (
	"bytes"
	"errors"
	"log"
	"sort"
	"sync"
	"time"
)

// Hub hält alle verbundenen Sessions und die Raumliste.
type Hub struct {
	// Updates kennt die neueste App-Version (nil = keine Hinweise).
	Updates  *UpdateChecker
	store    *Store
	certHash []byte
	maxUsers int
	limiter  *IPLimiter
	pending  chan struct{} // begrenzt gleichzeitige, noch nicht angemeldete Verbindungen

	mu         sync.RWMutex
	sessions   map[int64]*Session
	rooms      []DBRoom // Räume aus der Datenbank
	temp       []*tempRoom
	nextTempID int64
	pwLimiter  *IPLimiter // Passwort-Fehlversuche pro Nutzer
}

func NewHub(store *Store, certHash []byte, maxUsers int) (*Hub, error) {
	rooms, err := store.Rooms()
	if err != nil {
		return nil, err
	}
	return &Hub{
		store:      store,
		certHash:   certHash,
		maxUsers:   maxUsers,
		limiter:    NewIPLimiter(10, 10*time.Minute),
		pending:    make(chan struct{}, 64),
		sessions:   map[int64]*Session{},
		rooms:      rooms,
		nextTempID: tempRoomFirstID - 1,
		pwLimiter:  NewIPLimiter(passwordTries, passwordWindow),
	}, nil
}

func (h *Hub) defaultRoomLocked() int64 {
	if len(h.rooms) == 0 {
		return 0
	}
	return h.rooms[0].ID
}

func (h *Hub) roomExistsLocked(id int64) bool {
	for _, r := range h.rooms {
		if r.ID == id {
			return true
		}
	}
	return h.tempByIDLocked(id) != nil
}

func (h *Hub) usersLocked() []WireUser {
	out := make([]WireUser, 0, len(h.sessions))
	for _, s := range h.sessions {
		out = append(out, s.wireLocked())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (h *Hub) broadcastLocked(e *Envelope, except *Session) {
	frame, err := EncodeFrame(e)
	if err != nil {
		log.Printf("broadcast: %v", err)
		return
	}
	for _, s := range h.sessions {
		if s != except {
			s.sendRaw(frame)
		}
	}
}

// Add registriert eine angemeldete Session. Eine ältere Session desselben Nutzers wird beendet.
// Liefert false, wenn der Server voll ist.
func (h *Hub) Add(s *Session) (old *Session, ok bool) {
	_, mod := h.store.Moderator(s.userID)
	admin := h.store.LinkedToAdmin(s.userID)
	h.mu.Lock()
	defer h.mu.Unlock()
	old = h.sessions[s.userID]
	if old == nil && len(h.sessions) >= h.maxUsers {
		return nil, false
	}
	if old != nil {
		delete(h.sessions, s.userID)
		h.broadcastLocked(&Envelope{T: "user_left", ID: old.userID}, old)
		h.dropEmptyTempLocked(old.room)
	}
	s.room = h.defaultRoomLocked()
	h.sessions[s.userID] = s
	welcome := &Envelope{T: "welcome", You: ptr(s.wireLocked()), Rooms: h.allRoomsLocked(), Users: h.usersLocked()}
	if mod {
		welcome.Mod = boolPtr(true)
	}
	if admin {
		welcome.Admin = boolPtr(true)
	}
	welcome.ChatHours = int(h.store.ChatKeep / time.Hour)
	if up := h.Updates.Get(); up.Version != "" {
		welcome.Latest, welcome.LatestURL = up.Version, up.URL
	}
	s.send(welcome)
	h.broadcastLocked(&Envelope{T: "user_joined", User: ptr(s.wireLocked())}, s)
	return old, true
}

func (h *Hub) Remove(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[s.userID] != s {
		return // schon durch neuere Session ersetzt
	}
	delete(h.sessions, s.userID)
	h.broadcastLocked(&Envelope{T: "user_left", ID: s.userID}, nil)
	h.dropEmptyTempLocked(s.room)
}

// Join wechselt s in den Raum. Gesperrte temporäre Räume brauchen das Passwort;
// Nutzer mit verknüpftem Admin-Konto kommen ohne hinein. Fehler: errNoRoom,
// errRoomPassword, errTooManyTries.
func (h *Hub) Join(s *Session, room int64, password string) error {
	h.mu.RLock()
	tr := h.tempByIDLocked(room)
	needPw := tr != nil && tr.locked() && s.room != room
	h.mu.RUnlock()
	skip, reserved := !needPw, false
	if needPw {
		skip = h.store.LinkedToAdmin(s.userID)
	}
	if !skip {
		if !h.pwLimiter.Reserve(passwordKey(s.userID)) {
			return errTooManyTries
		}
		reserved = true
	}
	ok := false
	defer func() {
		if reserved && ok {
			h.pwLimiter.Refund(passwordKey(s.userID))
		}
	}()
	if len(password) > maxJoinPassword {
		password = password[:maxJoinPassword]
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.roomExistsLocked(room) || h.sessions[s.userID] != s {
		return errNoRoom
	}
	if tr := h.tempByIDLocked(room); tr != nil && s.room != room && !skip && !tr.check(password) {
		return errRoomPassword
	}
	ok = true
	if s.room != room {
		old := s.room
		s.room = room
		h.broadcastLocked(&Envelope{T: "user_update", User: ptr(s.wireLocked())}, nil)
		h.dropEmptyTempLocked(old)
	}
	return nil
}

func (h *Hub) SetState(s *Session, muted, deaf bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[s.userID] != s || (s.muted == muted && s.deaf == deaf) {
		return
	}
	s.muted, s.deaf = muted, deaf
	h.broadcastLocked(&Envelope{T: "user_update", User: ptr(s.wireLocked())}, nil)
}

func (h *Hub) Room(s *Session) int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return s.room
}

// Chat speichert und verteilt eine Nachricht an alle im selben Raum.
func (h *Hub) Chat(s *Session, text string) {
	h.mu.RLock()
	if h.sessions[s.userID] != s {
		h.mu.RUnlock()
		return
	}
	room := s.room
	name := s.name
	isTemp := h.tempByIDLocked(room) != nil
	h.mu.RUnlock()
	m := DBMessage{Room: room, From: s.userID, Name: name, Text: text, TS: time.Now().Unix()}
	if isTemp {
		// Temporäre Räume: Chat nur im Speicher, nie in der Datenbank
		h.mu.Lock()
		if tr := h.tempByIDLocked(room); tr != nil {
			tr.msgs = append(tr.msgs, m)
			if len(tr.msgs) > tempChatKeep {
				tr.msgs = tr.msgs[len(tr.msgs)-tempChatKeep:]
			}
		}
		h.mu.Unlock()
	} else if err := h.store.AddMessage(m, 200); err != nil {
		log.Printf("chat speichern: %v", err)
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	frame, err := EncodeFrame(&Envelope{T: "chat", Msg: &m})
	if err != nil {
		return
	}
	for _, o := range h.sessions {
		if o.room == room {
			o.sendRaw(frame)
		}
	}
}

// ForwardVoice leitet ein Sprachpaket an alle anderen im Raum weiter.
// pkt ist bereits im Server→Client-Format.
func (h *Hub) ForwardVoice(from *Session, pkt []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if from.muted || h.sessions[from.userID] != from {
		return
	}
	for _, o := range h.sessions {
		if o != from && o.room == from.room && !o.deaf && !o.ignored[from.userID] {
			_ = o.conn.SendDatagram(pkt) // Verlust ist bei Sprache in Ordnung
		}
	}
}

// maxIgnored begrenzt die Liste, damit ein Client den Server nicht mit Speicher flutet.
const maxIgnored = 500

// SetIgnored ersetzt die Liste der Nutzer, die s für sich stumm geschaltet hat.
// Ihre Sprache wird an s gar nicht erst geschickt (spart Daten).
func (h *Hub) SetIgnored(s *Session, ids []int64) {
	if len(ids) > maxIgnored {
		ids = ids[:maxIgnored]
	}
	set := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id > 0 && id != s.userID {
			set[id] = true
		}
	}
	h.mu.Lock()
	s.ignored = set
	h.mu.Unlock()
}

// --- Admin-Operationen ---

func (h *Hub) OnlineIDs() map[int64]bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[int64]bool, len(h.sessions))
	for id := range h.sessions {
		out[id] = true
	}
	return out
}

// Online liefert alle verbundenen Nutzer mit Raum und Status.
func (h *Hub) Online() []WireUser {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.usersLocked()
}

// OnlineInfo: wer ist da, mit Gerät und ob gerade gesprochen wird (für die Verwaltungsseite).
type OnlineInfo struct {
	WireUser
	Device  string
	Talking bool
}

func (h *Hub) OnlineDetails() []OnlineInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	now := time.Now().UnixMilli()
	out := make([]OnlineInfo, 0, len(h.sessions))
	for _, s := range h.sessions {
		out = append(out, OnlineInfo{
			WireUser: s.wireLocked(),
			Device:   s.device,
			Talking:  !s.muted && now-s.lastVoice.Load() < 1500,
		})
	}
	return out
}

func (h *Hub) deviceLabel(pk []byte) string {
	if d, err := h.store.DeviceByPubKey(pk); err == nil {
		return d.Label
	}
	return ""
}

func (h *Hub) OnlineCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}

func (h *Hub) Kick(userID int64, reason string) bool {
	h.mu.RLock()
	s := h.sessions[userID]
	h.mu.RUnlock()
	if s == nil {
		return false
	}
	s.kick(reason)
	return true
}

// Moderate wirft target raus (ban = auch sperren), wenn s das über sein Webkonto darf.
// Der Server prüft bei jeder Aktion neu; das mod-Flag im welcome ist nur für die Anzeige.
func (h *Hub) Moderate(s *Session, target int64, ban bool) {
	fail := func(msg string) { s.send(&Envelope{T: "error", Code: "forbidden", Reason: msg}) }
	acc, ok := h.store.Moderator(s.userID)
	if !ok {
		fail("Das darfst du nicht.")
		return
	}
	if target == s.userID {
		fail("Dich selbst kannst du nicht rauswerfen.")
		return
	}
	u, err := h.store.UserByID(target)
	if err != nil {
		fail("Diesen Nutzer gibt es nicht mehr.")
		return
	}
	if !acc.IsAdmin() && h.store.LinkedToAdmin(target) {
		fail("Admins kannst du nicht rauswerfen.")
		return
	}
	h.mu.RLock()
	by := s.name
	h.mu.RUnlock()
	if ban {
		if _, err := h.store.SetBanned(u.Name, true); err != nil {
			fail("Sperren hat nicht geklappt.")
			return
		}
		h.Kick(target, by+" hat dich gesperrt.")
		h.store.Audit(acc.Email+" (App)", "Talkie-Nutzer "+u.Name+" gesperrt")
		s.send(&Envelope{T: "info", Reason: u.Name + " ist gesperrt."})
		return
	}
	if !h.Kick(target, by+" hat dich rausgeworfen.") {
		fail(u.Name + " ist nicht online.")
		return
	}
	h.store.Audit(acc.Email+" (App)", "Talkie-Nutzer "+u.Name+" rausgeworfen")
	s.send(&Envelope{T: "info", Reason: u.Name + " wurde rausgeworfen."})
}

// deviceCodeValid: so lange gilt ein Geräte-Code aus der App.
const deviceCodeValid = 15 * time.Minute

// SendDeviceCode erzeugt einen Code, mit dem s ein weiteres Gerät anmelden kann.
func (h *Hub) SendDeviceCode(s *Session) {
	inv, err := h.store.CreateDeviceCode(s.userID, deviceCodeValid)
	switch {
	case errors.Is(err, ErrTooManyDevices):
		s.send(&Envelope{T: "error", Code: "too_many_devices", Reason: "Du hast schon 5 Geräte. Lass erst eins auf der Verwaltungsseite entfernen."})
	case err != nil:
		s.send(&Envelope{T: "error", Code: "internal", Reason: "Das hat nicht geklappt."})
	default:
		s.send(&Envelope{T: "device_code", Code: inv.Code})
	}
}

// KickDevice trennt die Verbindung, falls sie von diesem Gerät kommt (Gerät wurde entfernt).
func (h *Hub) KickDevice(userID int64, pk []byte, reason string) {
	h.mu.RLock()
	s := h.sessions[userID]
	h.mu.RUnlock()
	if s != nil && bytes.Equal(s.pubkey, pk) {
		s.kick(reason)
	}
}

// ClearChat leert den Chat eines Raums (0 = alle) und sagt es allen Apps.
func (h *Hub) ClearChat(room int64) (int64, error) {
	n, err := h.store.ClearChat(room)
	if err != nil {
		return 0, err
	}
	h.mu.Lock()
	for _, tr := range h.temp {
		if room == 0 || tr.id == room {
			tr.msgs = nil
		}
	}
	h.broadcastLocked(&Envelope{T: "chat_cleared", Room: room}, nil)
	h.mu.Unlock()
	return n, nil
}

// ClearChatFrom: Leeren aus der App, nur mit verknüpftem Admin-Konto.
func (h *Hub) ClearChatFrom(s *Session, room int64) {
	accs, _ := h.store.LinkedAccounts(s.userID)
	var admin *Account
	for i := range accs {
		if accs[i].IsAdmin() {
			admin = &accs[i]
			break
		}
	}
	if admin == nil {
		s.send(&Envelope{T: "error", Code: "forbidden", Reason: "Das darfst du nicht."})
		return
	}
	name := "allen Räumen"
	if room != 0 {
		h.mu.RLock()
		tr := h.tempByIDLocked(room)
		h.mu.RUnlock()
		if tr != nil {
			name = "„" + tr.name + "“"
		} else {
			r, err := h.store.RoomByID(room)
			if err != nil {
				s.send(&Envelope{T: "error", Code: "not_found", Reason: "Diesen Raum gibt es nicht mehr."})
				return
			}
			name = "„" + r.Name + "“"
		}
	}
	if _, err := h.ClearChat(room); err != nil {
		s.send(&Envelope{T: "error", Code: "internal", Reason: "Leeren hat nicht geklappt."})
		return
	}
	h.store.Audit(admin.Email+" (App)", "Chat in "+name+" geleert")
	s.send(&Envelope{T: "info", Reason: "Chat in " + name + " geleert."})
}

func (h *Hub) Renamed(userID int64, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.sessions[userID]; s != nil {
		s.name = name
		h.broadcastLocked(&Envelope{T: "user_update", User: ptr(s.wireLocked())}, nil)
	}
}

// ReloadRooms liest die Räume neu ein; wer in einem gelöschten Raum war, landet im ersten Raum.
func (h *Hub) ReloadRooms() error {
	rooms, err := h.store.Rooms()
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rooms = rooms // temporäre Räume bleiben erhalten
	h.broadcastLocked(&Envelope{T: "rooms", Rooms: h.allRoomsLocked()}, nil)
	def := h.defaultRoomLocked()
	for _, s := range h.sessions {
		if !h.roomExistsLocked(s.room) {
			s.room = def
			h.broadcastLocked(&Envelope{T: "user_update", User: ptr(s.wireLocked())}, nil)
		}
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
