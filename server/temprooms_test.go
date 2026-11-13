package main

import (
	"strings"
	"testing"
	"time"
)

func tempCount(h *Hub) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.temp)
}

func waitTemp(t *testing.T, h *Hub, want int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if tempCount(h) == want {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("temporäre Räume: %d, erwartet %d", tempCount(h), want)
}

func findRoom(rooms []DBRoom, name string) *DBRoom {
	for i := range rooms {
		if rooms[i].Name == name {
			return &rooms[i]
		}
	}
	return nil
}

func TestTempRoomCreateAndDelete(t *testing.T) {
	srv := startTestServer(t)
	inv, _ := srv.store.CreateInvite(3, time.Hour)
	a, wa := login(t, srv.addr, newKey(t), "Anna", inv.Code)
	b, _ := login(t, srv.addr, newKey(t), "Ben", inv.Code)
	dbRooms := len(wa.Rooms)

	writeEnv(t, a.stream, &Envelope{T: "create_room", Name: "Geheim", Password: "pudding"})
	r := b.expect("rooms")
	g := findRoom(r.Rooms, "Geheim")
	if g == nil || !g.Temp || !g.Locked || g.ID < tempRoomFirstID || len(r.Rooms) != dbRooms+1 {
		t.Fatalf("rooms: %+v", r.Rooms)
	}
	if u := b.expect("user_update"); u.User.Name != "Anna" || u.User.Room != g.ID {
		t.Fatalf("Ersteller muss im Raum sein: %+v", u)
	}
	// DB-Räume sind weder temp noch gesperrt, Passwort nie auf der Leitung
	for _, rm := range r.Rooms[:dbRooms] {
		if rm.Temp || rm.Locked {
			t.Fatalf("DB-Raum markiert: %+v", rm)
		}
	}
	// Chat im temporären Raum landet nicht in der Datenbank
	writeEnv(t, a.stream, &Envelope{T: "chat", Text: "geheim"})
	a.expect("chat")
	if h, _ := srv.store.History(g.ID, 50); len(h) != 0 {
		t.Fatal("Chat temporärer Räume gehört nicht in die DB")
	}

	// falsches / fehlendes Passwort
	writeEnv(t, b.stream, &Envelope{T: "join", Room: g.ID, Password: "falsch"})
	if e := b.expect("error"); e.Code != "room_password" {
		t.Fatalf("falsch: %+v", e)
	}
	writeEnv(t, b.stream, &Envelope{T: "join", Room: g.ID})
	if e := b.expect("error"); e.Code != "room_password" {
		t.Fatalf("leer: %+v", e)
	}
	// richtiges Passwort: rein, Verlauf kommt
	writeEnv(t, b.stream, &Envelope{T: "join", Room: g.ID, Password: "pudding"})
	h := b.expect("history")
	if h.Room != g.ID || len(h.Messages) != 1 || h.Messages[0].Text != "geheim" {
		t.Fatalf("history: %+v", h)
	}

	// Anna geht, Ben bleibt → Raum bleibt
	writeEnv(t, a.stream, &Envelope{T: "join", Room: wa.Rooms[0].ID})
	a.expect("history")
	if tempCount(srv.hub) != 1 {
		t.Fatal("Raum darf nicht verschwinden, Ben ist noch drin")
	}
	// Ben geht → Raum weg, rooms-Broadcast
	writeEnv(t, b.stream, &Envelope{T: "join", Room: wa.Rooms[0].ID})
	waitTemp(t, srv.hub, 0)
	for {
		e := a.expect("rooms")
		if findRoom(e.Rooms, "Geheim") == nil {
			break
		}
	}
}

func TestTempRoomDeletedOnDisconnectAndOpenRoom(t *testing.T) {
	srv := startTestServer(t)
	inv, _ := srv.store.CreateInvite(2, time.Hour)
	a, _ := login(t, srv.addr, newKey(t), "Anna", inv.Code)
	writeEnv(t, a.stream, &Envelope{T: "create_room", Name: "Offen"})
	a.expect("rooms")
	srv.hub.mu.RLock()
	open := srv.hub.temp[0].locked()
	srv.hub.mu.RUnlock()
	if open {
		t.Fatal("ohne Passwort darf der Raum nicht gesperrt sein")
	}
	a.conn.CloseWithError(0, "")
	waitTemp(t, srv.hub, 0)
}

func TestTempRoomAdminBypassAndKick(t *testing.T) {
	srv := startTestServer(t)
	inv, _ := srv.store.CreateInvite(3, time.Hour)
	kChef := newKey(t)
	c0, wc := login(t, srv.addr, kChef, "Chef", inv.Code)
	a, _ := login(t, srv.addr, newKey(t), "Anna", inv.Code)
	c0.conn.CloseWithError(0, "")
	if _, err := srv.store.AddAccount(Account{Email: "chef@x.de", Role: RoleAdmin, TalkieID: wc.You.ID}); err != nil {
		t.Fatal(err)
	}
	chef, _ := login(t, srv.addr, kChef, "", "")
	writeEnv(t, a.stream, &Envelope{T: "create_room", Name: "Privat", Password: "x"})
	r := chef.expect("rooms")
	g := findRoom(r.Rooms, "Privat")
	writeEnv(t, chef.stream, &Envelope{T: "join", Room: g.ID})
	chef.expect("history") // ohne Passwort drin
	// Anna wird getrennt (Kick), Chef bleibt → Raum bleibt; Chef geht → Raum weg
	srv.hub.Kick(wc.You.ID+1, "test")
	time.Sleep(600 * time.Millisecond)
	if tempCount(srv.hub) != 1 {
		t.Fatal("Chef ist noch drin")
	}
	chef.conn.CloseWithError(0, "")
	waitTemp(t, srv.hub, 0)
}

func TestTempRoomLimitsAndNames(t *testing.T) {
	srv := startTestServer(t)
	inv, _ := srv.store.CreateInvite(3, time.Hour)
	a, wa := login(t, srv.addr, newKey(t), "Anna", inv.Code)
	b, _ := login(t, srv.addr, newKey(t), "Ben", inv.Code)

	// Name kollidiert mit DB-Raum (Groß/Klein egal)
	writeEnv(t, a.stream, &Envelope{T: "create_room", Name: strings.ToUpper(wa.Rooms[0].Name)})
	if e := a.expect("error"); e.Code != "room_exists" {
		t.Fatalf("DB-Kollision: %+v", e)
	}
	writeEnv(t, a.stream, &Envelope{T: "create_room", Name: "x"})
	if e := a.expect("error"); e.Code != "name_invalid" {
		t.Fatalf("Name: %+v", e)
	}
	writeEnv(t, a.stream, &Envelope{T: "create_room", Name: "Ok", Password: strings.Repeat("a", 65)})
	if e := a.expect("error"); e.Code != "password_invalid" {
		t.Fatalf("Passwort: %+v", e)
	}
	writeEnv(t, a.stream, &Envelope{T: "create_room", Name: "Eins"})
	a.expect("rooms")
	// zweiter Raum desselben Erstellers
	writeEnv(t, a.stream, &Envelope{T: "create_room", Name: "Zwei"})
	if e := a.expect("error"); e.Code != "room_limit" {
		t.Fatalf("pro Ersteller: %+v", e)
	}
	// gleicher Name (anderer Ersteller)
	writeEnv(t, b.stream, &Envelope{T: "create_room", Name: "eins"})
	if e := b.expect("error"); e.Code != "room_exists" {
		t.Fatalf("temp-Kollision: %+v", e)
	}
	// Gesamtgrenze
	srv.hub.mu.Lock()
	for i := len(srv.hub.temp); i < maxTempRooms; i++ {
		srv.hub.temp = append(srv.hub.temp, &tempRoom{id: int64(tempRoomFirstID + 100 + i), name: "F" + string(rune('a'+i)), creator: 9999 + int64(i)})
	}
	srv.hub.mu.Unlock()
	writeEnv(t, b.stream, &Envelope{T: "create_room", Name: "Zuviel"})
	if e := b.expect("error"); e.Code != "room_limit" {
		t.Fatalf("gesamt: %+v", e)
	}
}

func TestTempRoomPasswordRateLimit(t *testing.T) {
	srv := startTestServer(t)
	inv, _ := srv.store.CreateInvite(2, time.Hour)
	a, _ := login(t, srv.addr, newKey(t), "Anna", inv.Code)
	b, _ := login(t, srv.addr, newKey(t), "Ben", inv.Code)
	writeEnv(t, a.stream, &Envelope{T: "create_room", Name: "Zu", Password: "richtig"})
	r := b.expect("rooms")
	id := findRoom(r.Rooms, "Zu").ID
	for i := 0; i < passwordTries; i++ {
		writeEnv(t, b.stream, &Envelope{T: "join", Room: id, Password: "nein"})
		if e := b.expect("error"); e.Code != "room_password" {
			t.Fatalf("Versuch %d: %+v", i, e)
		}
	}
	// ab jetzt gesperrt, auch mit dem richtigen Passwort
	writeEnv(t, b.stream, &Envelope{T: "join", Room: id, Password: "richtig"})
	if e := b.expect("error"); e.Code != "rate_limited" {
		t.Fatalf("Sperre: %+v", e)
	}
}

func TestReloadRoomsKeepsTemp(t *testing.T) {
	srv := startTestServer(t)
	inv, _ := srv.store.CreateInvite(2, time.Hour)
	a, _ := login(t, srv.addr, newKey(t), "Anna", inv.Code)
	writeEnv(t, a.stream, &Envelope{T: "create_room", Name: "Bleibt"})
	a.expect("rooms")
	if _, err := srv.store.AddRoom("Neu"); err != nil {
		t.Fatal(err)
	}
	if err := srv.hub.ReloadRooms(); err != nil {
		t.Fatal(err)
	}
	r := a.expect("rooms")
	if findRoom(r.Rooms, "Bleibt") == nil || findRoom(r.Rooms, "Neu") == nil {
		t.Fatalf("rooms: %+v", r.Rooms)
	}
	srv.hub.mu.RLock()
	defer srv.hub.mu.RUnlock()
	if srv.hub.sessions[1] == nil || srv.hub.sessions[1].room < tempRoomFirstID {
		t.Fatal("Anna muss im temporären Raum bleiben")
	}
	if srv.hub.defaultRoomLocked() >= tempRoomFirstID {
		t.Fatal("Standardraum muss ein DB-Raum sein")
	}
}
