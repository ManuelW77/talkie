package main

import (
	"log"
	"sort"
	"sync"
	"time"
)

// Hub hält alle verbundenen Sessions und die Raumliste.
type Hub struct {
	store    *Store
	certHash []byte
	maxUsers int
	limiter  *IPLimiter
	pending  chan struct{} // begrenzt gleichzeitige, noch nicht angemeldete Verbindungen

	mu       sync.RWMutex
	sessions map[int64]*Session
	rooms    []DBRoom
}

func NewHub(store *Store, certHash []byte, maxUsers int) (*Hub, error) {
	rooms, err := store.Rooms()
	if err != nil {
		return nil, err
	}
	return &Hub{
		store:    store,
		certHash: certHash,
		maxUsers: maxUsers,
		limiter:  NewIPLimiter(10, 10*time.Minute),
		pending:  make(chan struct{}, 64),
		sessions: map[int64]*Session{},
		rooms:    rooms,
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
	return false
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
	h.mu.Lock()
	defer h.mu.Unlock()
	old = h.sessions[s.userID]
	if old == nil && len(h.sessions) >= h.maxUsers {
		return nil, false
	}
	if old != nil {
		delete(h.sessions, s.userID)
		h.broadcastLocked(&Envelope{T: "user_left", ID: old.userID}, old)
	}
	s.room = h.defaultRoomLocked()
	h.sessions[s.userID] = s
	welcome := &Envelope{T: "welcome", You: ptr(s.wireLocked()), Rooms: h.rooms, Users: h.usersLocked()}
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
}

func (h *Hub) Join(s *Session, room int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.roomExistsLocked(room) || h.sessions[s.userID] != s {
		return false
	}
	if s.room != room {
		s.room = room
		h.broadcastLocked(&Envelope{T: "user_update", User: ptr(s.wireLocked())}, nil)
	}
	return true
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
	h.mu.RUnlock()
	m := DBMessage{Room: room, From: s.userID, Name: name, Text: text, TS: time.Now().Unix()}
	if err := h.store.AddMessage(m, 200); err != nil {
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
		if o != from && o.room == from.room && !o.deaf {
			_ = o.conn.SendDatagram(pkt) // Verlust ist bei Sprache in Ordnung
		}
	}
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
	h.rooms = rooms
	h.broadcastLocked(&Envelope{T: "rooms", Rooms: rooms}, nil)
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
