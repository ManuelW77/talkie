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
	for i, n := range []string{"Jonas", "Lea", "Finn"} {
		inv, _ := wt.store.CreateInvite(1, time.Hour)
		u, _ := wt.store.Register(inv.Code, n, []byte("k"+n))
		if i == 2 {
			wt.store.SetBanned(u.Name, true)
		}
	}
	wt.store.CreateInvite(3, 72*time.Hour)
	rooms, _ := wt.store.Rooms()
	wt.store.AddMessage(DBMessage{Room: rooms[0].ID, From: 1, Name: "Jonas", Text: "Wer ist dabei? 🎮", TS: time.Now().Unix()}, 200)
	wt.store.AddMessage(DBMessage{Room: rooms[0].ID, From: 2, Name: "Lea", Text: "Ich, gleich!", TS: time.Now().Unix()}, 200)
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
	for _, p := range []string{"/", "/users", "/users/1", "/rooms", "/chat", "/accounts", "/accounts/2", "/audit"} {
		_, _, body := b.get(p)
		save(strings.NewReplacer("/", "_").Replace(strings.Trim(p, "/")+"x"), body)
	}
	_, _, body = b.get(loc)
	save("invites", body)
}
