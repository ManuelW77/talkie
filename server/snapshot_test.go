package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Nur lokal: TALKIE_SNAPSHOT=dir go test -run Snapshot speichert die Seiten als HTML.
func TestSnapshot(t *testing.T) {
	dir := os.Getenv("TALKIE_SNAPSHOT")
	if dir == "" {
		t.Skip()
	}
	wt := newWebTest(t, &OIDCConfig{Issuer: "http://x", ClientID: "c", Name: "Pocket ID"})
	wt.account("manuel@example.org", RoleAdmin, 0)
	wt.account("oma@example.org", RoleUser, PermInvite|PermChat)
	devices := map[string]string{"Jonas": "Windows", "Lea": "iPhone/iPad", "Mia": "Android", "Finn": "Mac"}
	var users []*DBUser
	for i, n := range []string{"Jonas", "Lea", "Mia", "Finn"} {
		inv, _ := wt.store.CreateInvite(1, time.Hour)
		u, _ := wt.store.RegisterDevice(inv.Code, n, []byte("k"+n), devices[n])
		users = append(users, u)
		if i == 3 {
			wt.store.SetBanned(u.Name, true)
		}
	}
	// Jonas hat ein zweites Gerät
	if c, err := wt.store.CreateDeviceCode(users[0].ID, time.Hour); err == nil {
		wt.store.RegisterDevice(c.Code, "", []byte("kJonas2"), "iPhone/iPad")
	}
	// Drei sind online, Lea spricht gerade, Mia ist stumm (Sitzungen ohne echte Verbindung)
	roomsNow, _ := wt.store.Rooms()
	for i, u := range users[:3] {
		s := &Session{hub: wt.hub, userID: u.ID, name: u.Name, room: roomsNow[0].ID, device: devices[u.Name],
			out: make(chan []byte, 64), done: make(chan struct{})}
		if i == 1 {
			s.lastVoice.Store(time.Now().UnixMilli() + 60_000)
		}
		if i == 2 {
			s.muted = true
			s.room = roomsNow[1].ID
		}
		wt.hub.sessions[u.ID] = s
	}
	wt.store.CreateInvite(3, 72*time.Hour)
	rooms, _ := wt.store.Rooms()
	wt.store.AddMessage(DBMessage{Room: rooms[0].ID, From: 1, Name: "Jonas", Text: "Wer ist dabei? 🎮", TS: time.Now().Unix()}, 200)
	wt.store.AddMessage(DBMessage{Room: rooms[0].ID, From: 2, Name: "Lea", Text: "Ich, gleich!", TS: time.Now().Unix()}, 200)
	wt.store.ChatKeep = 24 * time.Hour
	// Beispiel-Fingerabdruck statt lauter Nullen
	wt.hub.certHash = []byte{0x7c, 0xc5, 0xbd, 0x21, 0x14, 0x02, 0xd9, 0x64, 0xd9, 0xe5, 0xc0, 0x90, 0x2e, 0x80, 0x82, 0x52,
		0x86, 0x93, 0x9b, 0x14, 0x27, 0x59, 0x5f, 0x8b, 0x91, 0x9e, 0xc2, 0x3e, 0x11, 0x42, 0x63, 0xa7}
	static, _ := filepath.Abs("web/static")
	save := func(name, body string) {
		body = strings.ReplaceAll(body, `"/static/`, `"file://`+static+`/`)
		os.WriteFile(filepath.Join(dir, name+".html"), []byte(body), 0o644)
	}
	_, _, body := wt.browser().get("/login")
	save("login", body)
	b := wt.browser()
	b.login("manuel@example.org")
	tok := b.csrf()
	b.post("/invites", url.Values{"csrf": {tok}, "uses": {"1"}, "days": {"7"}})
	_, loc, _ := b.post("/invites", url.Values{"csrf": {tok}, "uses": {"2"}, "days": {"7"}})
	_, _, body = b.get(loc) // zeigt (und verbraucht) die Meldung „Einladung erstellt“
	save("invites", body)
	for _, p := range []string{"/", "/users", "/users/1", "/rooms", "/chat", "/accounts", "/accounts/2", "/audit"} {
		_, _, body := b.get(p)
		save(strings.NewReplacer("/", "_").Replace(strings.Trim(p, "/")+"x"), body)
	}
}
