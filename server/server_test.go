package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

type testServer struct {
	addr  string
	hub   *Hub
	store *Store
}

func startTestServer(t *testing.T) *testServer {
	t.Helper()
	dir := t.TempDir()
	cert, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	hub, err := NewHub(store, CertHash(cert), 10)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := quic.ListenAddr("127.0.0.1:0",
		&tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{ALPN}, MinVersion: tls.VersionTLS13},
		&quic.Config{EnableDatagrams: true, KeepAlivePeriod: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); store.Close() })
	go func() {
		for {
			c, err := ln.Accept(context.Background())
			if err != nil {
				return
			}
			go hub.HandleConn(c)
		}
	}()
	return &testServer{addr: ln.Addr().String(), hub: hub, store: store}
}

type testClient struct {
	t      *testing.T
	conn   *quic.Conn
	stream *quic.Stream
	frames chan *Envelope
}

func dial(t *testing.T, addr string) (*quic.Conn, *quic.Stream, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(ctx, addr,
		&tls.Config{InsecureSkipVerify: true, NextProtos: []string{ALPN}},
		&quic.Config{EnableDatagrams: true})
	if err != nil {
		t.Fatal(err)
	}
	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(conn.ConnectionState().TLS.PeerCertificates[0].Raw)
	return conn, st, h[:]
}

func writeEnv(t *testing.T, st *quic.Stream, e *Envelope) {
	t.Helper()
	f, err := EncodeFrame(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write(f); err != nil {
		t.Fatal(err)
	}
}

// login meldet an (register, wenn invite != ""), liefert Client und die erste Antwort.
func login(t *testing.T, addr string, key ed25519.PrivateKey, name, invite string) (*testClient, *Envelope) {
	t.Helper()
	conn, st, certHash := dial(t, addr)
	pub := key.Public().(ed25519.PublicKey)
	writeEnv(t, st, &Envelope{T: "hello", V: 1, PubKey: base64.StdEncoding.EncodeToString(pub)})
	ch, err := ReadFrame(st)
	if err != nil {
		t.Fatal(err)
	}
	if ch.T != "challenge" {
		return nil, ch
	}
	nonce, _ := base64.StdEncoding.DecodeString(ch.Nonce)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, authMessage(nonce, certHash)))
	if invite != "" {
		writeEnv(t, st, &Envelope{T: "register", Sig: sig, Name: name, Invite: invite})
	} else {
		writeEnv(t, st, &Envelope{T: "auth", Sig: sig})
	}
	first, err := ReadFrame(st)
	if err != nil {
		t.Fatal(err)
	}
	c := &testClient{t: t, conn: conn, stream: st, frames: make(chan *Envelope, 100)}
	go func() {
		for {
			e, err := ReadFrame(st)
			if err != nil {
				close(c.frames)
				return
			}
			c.frames <- e
		}
	}()
	t.Cleanup(func() { conn.CloseWithError(0, "") })
	return c, first
}

func (c *testClient) expect(typ string) *Envelope {
	c.t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case e, ok := <-c.frames:
			if !ok {
				c.t.Fatalf("verbindung zu, erwartet %q", typ)
			}
			if e.T == typ {
				return e
			}
		case <-timeout:
			c.t.Fatalf("timeout, erwartet %q", typ)
		}
	}
}

func newKey(t *testing.T) ed25519.PrivateKey {
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestRegisterChatVoice(t *testing.T) {
	srv := startTestServer(t)
	inv, err := srv.store.CreateInvite(2, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ka, kb := newKey(t), newKey(t)

	a, w := login(t, srv.addr, ka, "Anna", inv.Code)
	if w.T != "welcome" || w.You.Name != "Anna" || len(w.Rooms) != 3 {
		t.Fatalf("welcome falsch: %+v", w)
	}
	// Code klein und ohne Bindestrich muss auch gehen
	b, w2 := login(t, srv.addr, kb, "Ben", inv.Code[:4]+inv.Code[5:])
	if w2.T != "welcome" {
		t.Fatalf("Ben: %+v", w2)
	}
	if j := a.expect("user_joined"); j.User.Name != "Ben" {
		t.Fatalf("user_joined: %+v", j)
	}

	// Chat
	writeEnv(t, a.stream, &Envelope{T: "chat", Text: "  Hallo Ben  "})
	if m := b.expect("chat"); m.Msg.Text != "Hallo Ben" || m.Msg.Name != "Anna" {
		t.Fatalf("chat: %+v", m.Msg)
	}

	// Sprache: A → B mit A's ID
	time.Sleep(100 * time.Millisecond)
	pkt := []byte{DatagramVoice, 0, 7, 0xAA, 0xBB, 0xCC}
	if err := a.conn.SendDatagram(pkt); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d, err := b.conn.ReceiveDatagram(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d[0] != DatagramVoice || int64(binary.BigEndian.Uint32(d[1:5])) != w.You.ID ||
		binary.BigEndian.Uint16(d[5:7]) != 7 || string(d[7:]) != "\xAA\xBB\xCC" {
		t.Fatalf("datagram falsch: %x", d)
	}

	// B wechselt den Raum → A's Sprache kommt nicht mehr an
	writeEnv(t, b.stream, &Envelope{T: "join", Room: w.Rooms[1].ID})
	if h := b.expect("history"); h.Room != w.Rooms[1].ID {
		t.Fatalf("history: %+v", h)
	}
	a.expect("user_update")
	_ = a.conn.SendDatagram(pkt)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	if _, err := b.conn.ReceiveDatagram(ctx2); err == nil {
		t.Fatal("datagram trotz anderem Raum empfangen")
	}

	// Erneut anmelden ohne Einladung
	a2, w3 := login(t, srv.addr, ka, "", "")
	if w3.T != "welcome" || w3.You.ID != w.You.ID {
		t.Fatalf("re-login: %+v", w3)
	}
	_ = a2
	// alte Session wird gekickt
	a.expect("kicked")
}

func TestInviteRules(t *testing.T) {
	srv := startTestServer(t)
	inv, _ := srv.store.CreateInvite(1, time.Hour)

	_, e := login(t, srv.addr, newKey(t), "Max", "ZZZZ-ZZZZ")
	if e.T != "error" || e.Code != "invite_invalid" {
		t.Fatalf("falscher Code akzeptiert: %+v", e)
	}
	_, e = login(t, srv.addr, newKey(t), "x", inv.Code)
	if e.Code != "name_invalid" {
		t.Fatalf("kurzer Name: %+v", e)
	}
	_, e = login(t, srv.addr, newKey(t), "Max", inv.Code)
	if e.T != "welcome" {
		t.Fatalf("gültige Registrierung: %+v", e)
	}
	_, e = login(t, srv.addr, newKey(t), "Moritz", inv.Code)
	if e.Code != "invite_invalid" {
		t.Fatalf("Einladung zweimal benutzt: %+v", e)
	}
	_, e = login(t, srv.addr, newKey(t), "Fremder", "")
	if e.Code != "auth_failed" {
		t.Fatalf("unbekannter Schlüssel: %+v", e)
	}
}

func TestBan(t *testing.T) {
	srv := startTestServer(t)
	inv, _ := srv.store.CreateInvite(1, time.Hour)
	k := newKey(t)
	c, _ := login(t, srv.addr, k, "Lars", inv.Code)
	u, err := srv.store.SetBanned("lars", true) // Groß/klein egal
	if err != nil {
		t.Fatal(err)
	}
	srv.hub.Kick(u.ID, "weg")
	c.expect("kicked")
	_, e := login(t, srv.addr, k, "", "")
	if e.Code != "banned" {
		t.Fatalf("gesperrter Nutzer kam rein: %+v", e)
	}
}

func TestRateLimit(t *testing.T) {
	srv := startTestServer(t)
	for i := 0; i < 10; i++ {
		_, e := login(t, srv.addr, newKey(t), "Max", "AAAA-AAAA")
		if e.Code != "invite_invalid" {
			t.Fatalf("versuch %d: %+v", i, e)
		}
	}
	_, e := login(t, srv.addr, newKey(t), "Max", "AAAA-AAAA")
	if e.Code != "rate_limited" {
		t.Fatalf("keine Sperre nach 10 Fehlversuchen: %+v", e)
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{"  Jörg  Müller ": "Jörg Müller", "Max_1.": "Max_1."} {
		if got, ok := cleanName(in); !ok || got != want {
			t.Errorf("cleanName(%q) = %q, %v", in, got, ok)
		}
	}
	for _, bad := range []string{"a", "<script>", "123456789012345678901", "a\u0000b", "M\u0430x", "\uFF2Dax", "Ma\u200Bx", "\u0661\u0662"} {
		if _, ok := cleanName(bad); ok {
			t.Errorf("cleanName(%q) sollte ungültig sein", bad)
		}
	}
}

func TestCleanChatStripsFormatChars(t *testing.T) {
	if got, _ := cleanChat("a\u202Eb\u200Bc 👨\u200D👩"); got != "abc 👨\u200D👩" {
		t.Fatalf("cleanChat = %q", got)
	}
}

func TestLimiterReserve(t *testing.T) {
	l := NewIPLimiter(2, time.Minute)
	if !l.Reserve("x") || !l.Reserve("x") || l.Reserve("x") {
		t.Fatal("parallele Versuche nicht begrenzt")
	}
	l.Refund("x")
	if !l.Reserve("x") {
		t.Fatal("Refund gibt nicht frei")
	}
}

func TestHistoryFitsFrame(t *testing.T) {
	srv := startTestServer(t)
	inv, _ := srv.store.CreateInvite(1, time.Hour)
	rooms, _ := srv.store.Rooms()
	big := strings.Repeat("😀", 500) // 500 Zeichen à 4 Byte
	for i := 0; i < 60; i++ {
		if err := srv.store.AddMessage(DBMessage{Room: rooms[0].ID, From: 1, Name: "X", Text: big, TS: 1}, 200); err != nil {
			t.Fatal(err)
		}
	}
	c, _ := login(t, srv.addr, newKey(t), "Max", inv.Code)
	h := c.expect("history")
	if len(h.Messages) == 0 || len(h.Messages) >= 50 {
		t.Fatalf("verlauf nicht gekürzt: %d", len(h.Messages))
	}
	if h.Messages[len(h.Messages)-1].Text != big {
		t.Fatal("text verändert")
	}
}

// recvVoice wartet kurz auf ein Sprachpaket und liefert die Absender-ID (0 = nichts kam).
func recvVoice(t *testing.T, c *testClient, wait time.Duration) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	d, err := c.conn.ReceiveDatagram(ctx)
	if err != nil {
		return 0
	}
	return int64(binary.BigEndian.Uint32(d[1:5]))
}

func TestIgnoreOnlyForSelf(t *testing.T) {
	srv := startTestServer(t)
	inv, err := srv.store.CreateInvite(3, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a, wa := login(t, srv.addr, newKey(t), "Anna", inv.Code)
	b, _ := login(t, srv.addr, newKey(t), "Ben", inv.Code)
	c, wc := login(t, srv.addr, newKey(t), "Cleo", inv.Code)
	b.expect("user_joined")
	pkt := []byte{DatagramVoice, 0, 1, 0xAA}

	// Ben schaltet Anna für sich stumm (eigene ID und Unsinn werden ignoriert)
	writeEnv(t, b.stream, &Envelope{T: "ignore", IDs: []int64{wa.You.ID, -5, 0}})
	time.Sleep(150 * time.Millisecond)
	_ = a.conn.SendDatagram(pkt)
	if got := recvVoice(t, c, 2*time.Second); got != wa.You.ID {
		t.Fatalf("Cleo muss Anna weiter hören, bekam %d", got)
	}
	if got := recvVoice(t, b, 300*time.Millisecond); got != 0 {
		t.Fatalf("Ben hört Anna trotz Stummschaltung (%d)", got)
	}
	// Cleo hört Ben weiterhin
	_ = c.conn.SendDatagram(pkt)
	if got := recvVoice(t, b, 2*time.Second); got != wc.You.ID {
		t.Fatalf("Ben muss Cleo hören, bekam %d", got)
	}

	// Leere Liste hebt die Stummschaltung auf
	writeEnv(t, b.stream, &Envelope{T: "ignore"})
	time.Sleep(150 * time.Millisecond)
	_ = a.conn.SendDatagram(pkt)
	if got := recvVoice(t, b, 2*time.Second); got != wa.You.ID {
		t.Fatalf("Ben hört Anna nach Aufheben nicht (%d)", got)
	}
}

func TestModerationFromApp(t *testing.T) {
	srv := startTestServer(t)
	inv, err := srv.store.CreateInvite(4, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	keyMod := newKey(t)
	// Zuerst registrieren, dann Webkonten verknüpfen, dann neu anmelden (mod kommt im welcome)
	m0, wm := login(t, srv.addr, keyMod, "Mama", inv.Code)
	_, wa := login(t, srv.addr, newKey(t), "Anna", inv.Code)
	b, wb := login(t, srv.addr, newKey(t), "Ben", inv.Code)
	_, wc := login(t, srv.addr, newKey(t), "Chef", inv.Code)
	if wm.Mod != nil {
		t.Fatal("ohne verknüpftes Konto darf niemand moderieren")
	}
	m0.conn.CloseWithError(0, "")

	if _, err := srv.store.AddAccount(Account{Email: "mama@x.de", Role: RoleUser, Perms: PermUsersEdit, TalkieID: wm.You.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.AddAccount(Account{Email: "oma@x.de", Role: RoleUser, Perms: PermInvite, TalkieID: wb.You.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.AddAccount(Account{Email: "chef@x.de", Role: RoleAdmin, TalkieID: wc.You.ID}); err != nil {
		t.Fatal(err)
	}

	m, wm2 := login(t, srv.addr, keyMod, "", "")
	if wm2.Mod == nil || !*wm2.Mod {
		t.Fatalf("Mama muss moderieren dürfen: %+v", wm2)
	}

	// Ben hat nur „Kids einladen“ → darf nicht
	writeEnv(t, b.stream, &Envelope{T: "kick", ID: wa.You.ID})
	if e := b.expect("error"); e.Code != "forbidden" {
		t.Fatalf("Ben: %+v", e)
	}
	// Admin ist geschützt
	writeEnv(t, m.stream, &Envelope{T: "kick", ID: wc.You.ID})
	if e := m.expect("error"); !strings.Contains(e.Reason, "Admins") {
		t.Fatalf("Admin-Schutz: %+v", e)
	}
	// Ben rauswerfen
	writeEnv(t, m.stream, &Envelope{T: "kick", ID: wb.You.ID})
	if e := m.expect("info"); !strings.Contains(e.Reason, "rausgeworfen") {
		t.Fatalf("kick: %+v", e)
	}
	if k := b.expect("kicked"); !strings.Contains(k.Reason, "Mama") {
		t.Fatalf("Ben bekam: %+v", k)
	}
	// Anna sperren → fliegt raus und kommt nicht wieder rein
	writeEnv(t, m.stream, &Envelope{T: "ban", ID: wa.You.ID})
	if e := m.expect("info"); !strings.Contains(e.Reason, "gesperrt") {
		t.Fatalf("ban: %+v", e)
	}
	if u, _ := srv.store.UserByID(wa.You.ID); u == nil || !u.Banned {
		t.Fatal("Anna nicht gesperrt")
	}
	if log, _ := srv.store.AuditLog(10); len(log) == 0 || !strings.Contains(log[0].Who, "(App)") {
		t.Fatalf("Protokoll fehlt: %+v", log)
	}
}

func TestClearChatFromApp(t *testing.T) {
	srv := startTestServer(t)
	srv.store.ChatKeep = 24 * time.Hour
	inv, _ := srv.store.CreateInvite(2, time.Hour)
	kChef := newKey(t)
	c0, wc := login(t, srv.addr, kChef, "Chef", inv.Code)
	k, wk := login(t, srv.addr, newKey(t), "Kid", inv.Code)
	if wc.Admin != nil || wc.ChatHours != 24 {
		t.Fatalf("welcome: admin=%v hours=%d", wc.Admin, wc.ChatHours)
	}
	c0.conn.CloseWithError(0, "")
	if _, err := srv.store.AddAccount(Account{Email: "chef@x.de", Role: RoleAdmin, TalkieID: wc.You.ID}); err != nil {
		t.Fatal(err)
	}
	c, wc2 := login(t, srv.addr, kChef, "", "")
	if wc2.Admin == nil || !*wc2.Admin {
		t.Fatal("Chef muss admin-Flag bekommen")
	}
	writeEnv(t, k.stream, &Envelope{T: "chat", Text: "hallo"})
	k.expect("chat")
	// Kid darf nicht leeren
	writeEnv(t, k.stream, &Envelope{T: "clear_chat", Room: wk.You.Room})
	if e := k.expect("error"); e.Code != "forbidden" {
		t.Fatalf("Kid: %+v", e)
	}
	// Chef leert alles → alle Apps bekommen chat_cleared
	writeEnv(t, c.stream, &Envelope{T: "clear_chat"})
	if e := c.expect("info"); !strings.Contains(e.Reason, "geleert") {
		t.Fatalf("info: %+v", e)
	}
	if e := k.expect("chat_cleared"); e.Room != 0 {
		t.Fatalf("chat_cleared: %+v", e)
	}
	if h, _ := srv.store.History(wk.You.Room, 50); len(h) != 0 {
		t.Fatal("Chat nicht geleert")
	}
}

func TestMultipleDevices(t *testing.T) {
	srv := startTestServer(t)
	inv, _ := srv.store.CreateInvite(1, time.Hour)
	kLaptop, kPhone := newKey(t), newKey(t)

	laptop, w := login(t, srv.addr, kLaptop, "Anna", inv.Code)
	if w.T != "welcome" {
		t.Fatalf("%+v", w)
	}
	// Normale Einladung ohne Namen geht nicht
	inv2, _ := srv.store.CreateInvite(1, time.Hour)
	if _, e := login(t, srv.addr, newKey(t), "", inv2.Code); e.T != "error" || e.Code != "name_invalid" {
		t.Fatalf("ohne Namen: %+v", e)
	}

	// Laptop holt einen Geräte-Code
	writeEnv(t, laptop.stream, &Envelope{T: "device_code"})
	dc := laptop.expect("device_code")
	if len(dc.Code) != 9 {
		t.Fatalf("code: %+v", dc)
	}
	// iPhone meldet sich mit dem Code an, ohne Namen → ist Anna
	phone, wp := login(t, srv.addr, kPhone, "", dc.Code)
	if wp.T != "welcome" || wp.You.ID != w.You.ID || wp.You.Name != "Anna" {
		t.Fatalf("iPhone: %+v", wp)
	}
	if k := laptop.expect("kicked"); !strings.Contains(k.Reason, "anderen Gerät") {
		t.Fatalf("laptop: %+v", k)
	}
	// Code ist verbraucht
	if _, e := login(t, srv.addr, newKey(t), "", dc.Code); e.Code != "invite_invalid" {
		t.Fatalf("zweimal: %+v", e)
	}
	// Laptop kommt ohne Code wieder rein (eigener Schlüssel), iPhone fliegt
	_, wl := login(t, srv.addr, kLaptop, "", "")
	if wl.T != "welcome" || wl.You.ID != w.You.ID {
		t.Fatalf("laptop wieder: %+v", wl)
	}
	phone.expect("kicked")
	devs, _ := srv.store.Devices(w.You.ID)
	if len(devs) != 2 {
		t.Fatalf("geräte: %d", len(devs))
	}
	// Gerät entfernen: iPhone kommt nicht mehr rein, Laptop schon
	pk, _ := srv.store.DevicePubKey(w.You.ID, devs[len(devs)-1].ID)
	var phoneDev int64
	for _, d := range devs {
		if p, _ := srv.store.DevicePubKey(w.You.ID, d.ID); string(p) == string(kPhone.Public().(ed25519.PublicKey)) {
			phoneDev = d.ID
		}
	}
	_ = pk
	if err := srv.store.DeleteDevice(w.You.ID, phoneDev); err != nil {
		t.Fatal(err)
	}
	if _, e := login(t, srv.addr, kPhone, "", ""); e.T != "error" || e.Code != "auth_failed" {
		t.Fatalf("entferntes Gerät: %+v", e)
	}
	// Letztes Gerät lässt sich nicht entfernen
	devs, _ = srv.store.Devices(w.You.ID)
	if err := srv.store.DeleteDevice(w.You.ID, devs[0].ID); err == nil {
		t.Fatal("letztes Gerät entfernt")
	}
	// Höchstens MaxDevices
	for i := 0; i < MaxDevices-1; i++ {
		c, err := srv.store.CreateDeviceCode(w.You.ID, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if _, e := login(t, srv.addr, newKey(t), "", c.Code); e.T != "welcome" {
			t.Fatalf("gerät %d: %+v", i, e)
		}
	}
	if _, err := srv.store.CreateDeviceCode(w.You.ID, time.Hour); !errors.Is(err, ErrTooManyDevices) {
		t.Fatalf("mehr als %d geräte: %v", MaxDevices, err)
	}
	// Geräte-Codes tauchen nicht in der Einladungsliste auf
	invs, _ := srv.store.Invites()
	for _, i := range invs {
		if i.Code == dc.Code {
			t.Fatal("Geräte-Code in Einladungsliste")
		}
	}
	// Gesperrter Nutzer kommt auch mit Geräte-Code nicht rein
	u, _ := srv.store.UserByID(w.You.ID)
	devs, _ = srv.store.Devices(w.You.ID)
	_ = srv.store.DeleteDevice(w.You.ID, devs[len(devs)-1].ID)
	c, _ := srv.store.CreateDeviceCode(w.You.ID, time.Hour)
	srv.store.SetBanned(u.Name, true)
	if _, e := login(t, srv.addr, newKey(t), "", c.Code); e.Code != "banned" {
		t.Fatalf("gesperrt: %+v", e)
	}
}

func TestDevicesMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alt.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	// Nutzer wie in alten Versionen: nur users.pubkey, keine Geräte
	pk := make([]byte, 32)
	pk[0] = 7
	if _, err := s.db.Exec("INSERT INTO users(name, pubkey, created) VALUES('Alt', ?, 1)", pk); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, err := s.UserByPubKey(pk)
	if err != nil || u.Name != "Alt" {
		t.Fatalf("migration: %v %+v", err, u)
	}
	// Erstgerät entfernen (es gibt ein zweites) → darf nach Neustart nicht zurückkommen
	c, _ := s.CreateDeviceCode(u.ID, time.Hour)
	pk2 := make([]byte, 32)
	pk2[0] = 8
	if _, err := s.RegisterDevice(c.Code, "", pk2, "iOS"); err != nil {
		t.Fatal(err)
	}
	d, _ := s.DeviceByPubKey(pk)
	if err := s.DeleteDevice(u.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserByPubKey(pk); err == nil {
		t.Fatal("entferntes Erstgerät ist nach Neustart wieder da")
	}
	if _, err := s.UserByPubKey(pk2); err != nil {
		t.Fatal("zweites Gerät fehlt")
	}
}

func TestOnlineDetails(t *testing.T) {
	srv := startTestServer(t)
	inv, _ := srv.store.CreateInvite(1, time.Hour)
	conn, st, certHash := dial(t, srv.addr)
	key := newKey(t)
	pub := key.Public().(ed25519.PublicKey)
	writeEnv(t, st, &Envelope{T: "hello", V: 1, PubKey: base64.StdEncoding.EncodeToString(pub)})
	ch, _ := ReadFrame(st)
	nonce, _ := base64.StdEncoding.DecodeString(ch.Nonce)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, authMessage(nonce, certHash)))
	writeEnv(t, st, &Envelope{T: "register", Sig: sig, Name: "Lia", Invite: inv.Code, Device: "iPhone/iPad"})
	if w, _ := ReadFrame(st); w.T != "welcome" {
		t.Fatalf("%+v", w)
	}
	time.Sleep(100 * time.Millisecond)
	d := srv.hub.OnlineDetails()
	if len(d) != 1 || d[0].Device != "iPhone/iPad" || d[0].Talking {
		t.Fatalf("vorher: %+v", d)
	}
	_ = conn.SendDatagram([]byte{DatagramVoice, 0, 1, 0xAA})
	time.Sleep(150 * time.Millisecond)
	if d := srv.hub.OnlineDetails(); !d[0].Talking {
		t.Fatalf("spricht nicht: %+v", d)
	}
}
