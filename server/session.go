package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"log"
	"net"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/quic-go/quic-go"
)

const (
	handshakeTimeout = 15 * time.Second
	outQueueSize     = 256
	historySize      = 50
	maxChatRunes     = 500
	maxVoicePayload  = 1000
)

// Session ist eine angemeldete Client-Verbindung.
// room, muted, deaf und name werden durch hub.mu geschützt.
type Session struct {
	hub    *Hub
	conn   *quic.Conn
	stream *quic.Stream
	ip     string

	userID int64
	name   string
	room   int64
	muted  bool
	deaf   bool

	out       chan []byte
	closeOnce sync.Once
	done      chan struct{}
}

func (s *Session) wireLocked() WireUser {
	return WireUser{ID: s.userID, Name: s.name, Room: s.room, Muted: s.muted, Deaf: s.deaf}
}

func (s *Session) send(e *Envelope) {
	frame, err := EncodeFrame(e)
	if err != nil {
		log.Printf("encode: %v", err)
		return
	}
	s.sendRaw(frame)
}

// sendRaw blockiert nie. Ist die Warteschlange voll, ist der Client zu langsam
// oder hängt — dann wird die Verbindung getrennt.
func (s *Session) sendRaw(frame []byte) {
	select {
	case <-s.done:
	case s.out <- frame:
	default:
		go s.close(quic.ApplicationErrorCode(2), "zu langsam")
	}
}

func (s *Session) close(code quic.ApplicationErrorCode, msg string) {
	s.closeOnce.Do(func() {
		close(s.done)
		_ = s.conn.CloseWithError(code, msg)
	})
}

// kick schickt noch eine Begründung und trennt dann.
func (s *Session) kick(reason string) {
	s.send(&Envelope{T: "kicked", Reason: reason})
	time.AfterFunc(300*time.Millisecond, func() { s.close(quic.ApplicationErrorCode(3), "kicked") })
}

func (s *Session) writer() {
	for {
		select {
		case <-s.done:
			return
		case f := <-s.out:
			_ = s.stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := s.stream.Write(f); err != nil {
				s.close(quic.ApplicationErrorCode(1), "write")
				return
			}
		}
	}
}

func (s *Session) voiceReader(ctx context.Context) {
	limit := NewTokenBucket(50, 30) // 25 Pakete/s normal, etwas Luft für Jitter
	for {
		d, err := s.conn.ReceiveDatagram(ctx)
		if err != nil {
			return
		}
		if len(d) < 4 || d[0] != DatagramVoice || len(d)-3 > maxVoicePayload || !limit.Allow() {
			continue
		}
		pkt := make([]byte, 0, len(d)+4)
		pkt = append(pkt, DatagramVoice)
		pkt = binary.BigEndian.AppendUint32(pkt, uint32(s.userID))
		pkt = append(pkt, d[1:]...) // seq + opus
		s.hub.ForwardVoice(s, pkt)
	}
}

func (s *Session) controlLoop() {
	chatLimit := NewTokenBucket(5, 1)
	ctrlLimit := NewTokenBucket(20, 5)
	for {
		_ = s.stream.SetReadDeadline(time.Time{})
		e, err := ReadFrame(s.stream)
		if err != nil {
			return
		}
		if !ctrlLimit.Allow() {
			continue
		}
		switch e.T {
		case "join":
			if s.hub.Join(s, e.Room) {
				s.sendHistory(e.Room)
			}
		case "chat":
			text, ok := cleanChat(e.Text)
			if !ok {
				continue
			}
			if !chatLimit.Allow() {
				s.send(&Envelope{T: "error", Code: "rate_limited", Reason: "Nicht so schnell!"})
				continue
			}
			s.hub.Chat(s, text)
		case "state":
			muted, deaf := false, false
			if e.Muted != nil {
				muted = *e.Muted
			}
			if e.Deaf != nil {
				deaf = *e.Deaf
			}
			s.hub.SetState(s, muted, deaf)
		}
	}
}

func (s *Session) sendHistory(room int64) {
	msgs, err := s.hub.store.History(room, historySize)
	if err != nil {
		log.Printf("history: %v", err)
		return
	}
	// Älteste Nachrichten weglassen, bis der Verlauf in einen Frame passt
	for len(msgs) > 0 {
		if _, err := EncodeFrame(&Envelope{T: "history", Room: room, Messages: msgs}); err == nil {
			break
		}
		msgs = msgs[1:]
	}
	s.send(&Envelope{T: "history", Room: room, Messages: msgs})
}

// --- Verbindungsaufbau ---

func remoteIP(c *quic.Conn) string {
	if a, ok := c.RemoteAddr().(*net.UDPAddr); ok {
		if v4 := a.IP.To4(); v4 != nil {
			return v4.String()
		}
		// IPv6: ein Anschluss hat meist ein ganzes /64 — danach begrenzen
		return a.IP.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
	host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
	return host
}

func handshakeError(stream *quic.Stream, code, msg string) {
	if f, err := EncodeFrame(&Envelope{T: "error", Code: code, Reason: msg}); err == nil {
		_ = stream.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, _ = stream.Write(f)
	}
}

func authMessage(nonce, certHash []byte) []byte {
	m := make([]byte, 0, len(AuthContext)+len(nonce)+len(certHash))
	m = append(m, AuthContext...)
	m = append(m, nonce...)
	return append(m, certHash...)
}

// HandleConn führt Anmeldung durch und betreibt danach die Session.
func (h *Hub) HandleConn(conn *quic.Conn) {
	select {
	case h.pending <- struct{}{}:
	default:
		_ = conn.CloseWithError(5, "zu viele Anmeldungen")
		return
	}
	released := false
	release := func() {
		if !released {
			released = true
			<-h.pending
		}
	}
	defer release()
	ip := remoteIP(conn)
	ctx, cancel := context.WithTimeout(conn.Context(), handshakeTimeout)
	stream, err := conn.AcceptStream(ctx)
	cancel()
	if err != nil {
		_ = conn.CloseWithError(1, "kein Steuerkanal")
		return
	}
	closeLater := func() {
		time.AfterFunc(500*time.Millisecond, func() { _ = conn.CloseWithError(4, "anmeldung fehlgeschlagen") })
	}

	user, err := h.handshake(stream, ip)
	if err != nil {
		var he *hsError
		if errors.As(err, &he) {
			if he.countFail {
				h.limiter.Fail(ip)
			}
			log.Printf("anmeldung von %s abgelehnt: %s", ip, he.code)
			handshakeError(stream, he.code, he.msg)
		}
		closeLater()
		return
	}

	release()
	s := &Session{
		hub: h, conn: conn, stream: stream, ip: ip,
		userID: user.ID, name: user.Name,
		out:  make(chan []byte, outQueueSize),
		done: make(chan struct{}),
	}
	go s.writer()
	old, ok := h.Add(s)
	if !ok {
		s.send(&Envelope{T: "error", Code: "server_full", Reason: "Der Server ist voll."})
		time.AfterFunc(500*time.Millisecond, func() { s.close(4, "voll") })
		return
	}
	if old != nil {
		old.kick("Du hast dich an einem anderen Gerät angemeldet.")
	}
	// Sperre/Löschung während der Anmeldung? Erst jetzt ist die Session für Kick sichtbar.
	if cur, err := h.store.UserByPubKey(user.PubKey); err != nil || cur.Banned {
		s.kick("Du bist auf diesem Server gesperrt.")
	}
	_ = h.store.TouchUser(user.ID)
	log.Printf("%s (%d) verbunden von %s", user.Name, user.ID, ip)
	s.sendHistory(h.Room(s))

	go s.voiceReader(conn.Context())
	s.controlLoop()

	h.Remove(s)
	s.close(0, "tschüss")
	_ = h.store.TouchUser(user.ID)
	log.Printf("%s (%d) getrennt", user.Name, user.ID)
}

type hsError struct {
	code, msg string
	countFail bool
}

func (e *hsError) Error() string { return e.code }

func (h *Hub) handshake(stream *quic.Stream, ip string) (*DBUser, error) {
	_ = stream.SetDeadline(time.Now().Add(handshakeTimeout))
	defer stream.SetDeadline(time.Time{})

	if h.limiter.Blocked(ip) {
		return nil, &hsError{"rate_limited", "Zu viele Fehlversuche. Bitte warte 10 Minuten.", false}
	}
	hello, err := ReadFrame(stream)
	if err != nil {
		return nil, err
	}
	if hello.T != "hello" {
		return nil, &hsError{"bad_request", "Ungültige Anfrage.", true}
	}
	if hello.V != ProtocolVersion {
		return nil, &hsError{"version", "Bitte lade die neueste Talkie-Version herunter.", false}
	}
	pk, err := base64.StdEncoding.DecodeString(hello.PubKey)
	if err != nil || len(pk) != ed25519.PublicKeySize {
		return nil, &hsError{"bad_request", "Ungültiger Schlüssel.", true}
	}
	existing, err := h.store.UserByPubKey(pk)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	f, err := EncodeFrame(&Envelope{T: "challenge", Nonce: base64.StdEncoding.EncodeToString(nonce), Known: boolPtr(existing != nil)})
	if err != nil {
		return nil, err
	}
	if _, err := stream.Write(f); err != nil {
		return nil, err
	}

	resp, err := ReadFrame(stream)
	if err != nil {
		return nil, err
	}
	sig, err := base64.StdEncoding.DecodeString(resp.Sig)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(pk), authMessage(nonce, h.certHash), sig) {
		return nil, &hsError{"auth_failed", "Anmeldung fehlgeschlagen.", true}
	}

	switch {
	case existing != nil && resp.T == "auth":
		if existing.Banned {
			return nil, &hsError{"banned", "Du bist auf diesem Server gesperrt.", false}
		}
		return existing, nil
	case existing == nil && resp.T == "register":
		name, ok := cleanName(resp.Name)
		if !ok {
			return nil, &hsError{"name_invalid", "Der Name muss 2 bis 20 Zeichen lang sein (Buchstaben, Zahlen, Leerzeichen, - _ .).", false}
		}
		u, err := h.store.Register(resp.Invite, name, pk)
		switch {
		case errors.Is(err, ErrInviteInvalid):
			return nil, &hsError{"invite_invalid", "Der Einladungscode stimmt nicht oder ist abgelaufen.", true}
		case errors.Is(err, ErrNameTaken):
			return nil, &hsError{"name_taken", "Diesen Namen gibt es schon. Nimm bitte einen anderen.", false}
		case err != nil:
			return nil, err
		}
		log.Printf("neuer Nutzer registriert: %s (%d)", u.Name, u.ID)
		return u, nil
	case existing == nil && resp.T == "auth":
		return nil, &hsError{"auth_failed", "Dieses Gerät ist nicht mehr registriert. Bitte melde dich neu mit einem Einladungscode an.", false}
	default:
		return nil, &hsError{"bad_request", "Ungültige Anfrage.", true}
	}
}

func cleanName(n string) (string, bool) {
	n = strings.Join(strings.Fields(n), " ")
	l := utf8.RuneCountInString(n)
	if l < 2 || l > 20 {
		return "", false
	}
	for _, r := range n {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_' || r == '.') {
			return "", false
		}
	}
	return n, true
}

func cleanChat(t string) (string, bool) {
	t = strings.TrimSpace(t)
	if t == "" || !utf8.ValidString(t) {
		return "", false
	}
	t = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, t)
	if utf8.RuneCountInString(t) > maxChatRunes {
		t = string([]rune(t)[:maxChatRunes])
	}
	return t, t != ""
}
