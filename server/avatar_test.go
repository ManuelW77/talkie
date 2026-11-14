package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pngB64 erzeugt ein einfarbiges PNG (Größe w×h) als base64.
func pngB64(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 2), uint8(y * 2), 120, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

// expectError wartet auf ein error mit dem Code.
func (c *testClient) expectError(code string) {
	c.t.Helper()
	if e := c.expect("error"); e.Code != code {
		c.t.Fatalf("error %q, erwartet %q", e.Code, code)
	}
}

func twoClients(t *testing.T) (*testServer, *testClient, *testClient, *Envelope, *Envelope) {
	srv := startTestServer(t)
	inv, _ := srv.store.CreateInvite(5, time.Hour, "")
	a, wa := login(t, srv.addr, newKey(t), "Anna", inv.Code)
	b, wb := login(t, srv.addr, newKey(t), "Ben", inv.Code)
	if a == nil || b == nil || wa.T != "welcome" || wb.T != "welcome" {
		t.Fatal("anmeldung fehlgeschlagen")
	}
	return srv, a, b, wa, wb
}

func TestAvatarSetGet(t *testing.T) {
	srv, a, b, wa, _ := twoClients(t)
	if wa.You.Avatar != 0 {
		t.Fatal("neuer Nutzer hat schon ein Bild")
	}
	writeEnv(t, a.stream, &Envelope{T: "set_avatar", Data: pngB64(t, 128, 128)})
	var up *Envelope
	for {
		up = b.expect("user_update")
		if up.User.Name == "Anna" {
			break
		}
	}
	if up.User.Avatar <= 0 {
		t.Fatalf("user_update ohne avatar: %+v", up.User)
	}
	ver := up.User.Avatar

	// get_avatar liefert ein JPEG
	writeEnv(t, b.stream, &Envelope{T: "get_avatar", ID: up.User.ID})
	av := b.expect("avatar")
	if av.ID != up.User.ID || av.Avatar != ver {
		t.Fatalf("avatar: %+v", av)
	}
	raw, err := base64.StdEncoding.DecodeString(av.Data)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil || img.Bounds().Dx() != 128 || img.Bounds().Dy() != 128 {
		t.Fatalf("kein 128×128-JPEG: %v", err)
	}
	// Neue Anmeldung (welcome) kennt die Version
	if srv.store.AvatarVersion(up.User.ID) != ver {
		t.Fatal("version nicht gespeichert")
	}

	// Unbekannte id → leer
	writeEnv(t, b.stream, &Envelope{T: "get_avatar", ID: 9999})
	if av := b.expect("avatar"); av.Data != "" || av.Avatar != 0 || av.ID != 9999 {
		t.Fatalf("unbekannt: %+v", av)
	}
	// Nutzer ohne Bild → leer
	writeEnv(t, a.stream, &Envelope{T: "get_avatar", ID: wa.You.ID + 1})
	if av := a.expect("avatar"); av.Data != "" || av.Avatar != 0 {
		t.Fatalf("ohne Bild: %+v", av)
	}
}

func TestAvatarRemoveAndRateLimit(t *testing.T) {
	_, a, b, _, _ := twoClients(t)
	writeEnv(t, a.stream, &Envelope{T: "set_avatar", Data: pngB64(t, 128, 128)})
	// Zweites set_avatar sofort danach → rate_limited (Verbindung bleibt offen)
	writeEnv(t, a.stream, &Envelope{T: "set_avatar", Data: ""})
	a.expectError("rate_limited")
	var up *Envelope
	for up = b.expect("user_update"); up.User.Name != "Anna"; up = b.expect("user_update") {
	}
	if up.User.Avatar == 0 {
		t.Fatal("Bild fehlt")
	}
}

func TestAvatarRemoveByEmptyData(t *testing.T) {
	srv, a, _, wa, _ := twoClients(t)
	if _, err := srv.store.SetAvatar(wa.You.ID, []byte("x")); err != nil {
		t.Fatal(err)
	}
	writeEnv(t, a.stream, &Envelope{T: "set_avatar", Data: ""})
	up := a.expect("user_update")
	if up.User.ID != wa.You.ID || up.User.Avatar != 0 {
		t.Fatalf("entfernen: %+v", up.User)
	}
	if v, d := srv.store.Avatar(wa.You.ID); v != 0 || d != nil {
		t.Fatal("Bild noch da")
	}
}

func TestAvatarRejects(t *testing.T) {
	big := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, maxAvatarBytes+1))
	cases := map[string]string{
		"falsche Größe":     pngB64(t, 64, 64),
		"nicht quadratisch": pngB64(t, 128, 100),
		"zu groß":           big,
		"kein Bild":         base64.StdEncoding.EncodeToString([]byte("das ist kein bild")),
		"kein base64":       "%%%",
	}
	for name, data := range cases {
		if _, err := prepareAvatar(data); err == nil {
			t.Errorf("%s: sollte abgelehnt werden", name)
		}
	}
	// Über die Verbindung: error bad_avatar, Verbindung bleibt offen (jedes Mal neuer Nutzer wegen Rate-Limit)
	_, a, _, _, _ := twoClients(t)
	writeEnv(t, a.stream, &Envelope{T: "set_avatar", Data: pngB64(t, 64, 64)})
	a.expectError("bad_avatar")
	// Das abgelehnte Bild zählt gegen das Limit, die Verbindung lebt aber weiter
	writeEnv(t, a.stream, &Envelope{T: "members"})
	a.expect("members")
}

func TestAvatarJPEGAccepted(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareAvatar(base64.StdEncoding.EncodeToString(b.Bytes())); err != nil {
		t.Fatal(err)
	}
}

func TestAvatarVersionIncreases(t *testing.T) {
	wt := newWebTest(t, nil)
	inv, _ := wt.store.CreateInvite(1, time.Hour, "")
	u, _ := wt.store.Register(inv.Code, "Mia", bytes.Repeat([]byte{1}, 32))
	v1, err := wt.store.SetAvatar(u.ID, []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	v2, _ := wt.store.SetAvatar(u.ID, []byte("b"))
	if v2 <= v1 {
		t.Fatalf("version nicht größer: %d %d", v1, v2)
	}
}

func TestMembers(t *testing.T) {
	srv, a, b, wa, wb := twoClients(t)
	inv, _ := srv.store.CreateInvite(5, time.Hour, "")
	if _, err := srv.store.Register(inv.Code, "carla", bytes.Repeat([]byte{5}, 32)); err != nil {
		t.Fatal(err)
	}
	bad, err := srv.store.Register(inv.Code, "Zora", bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.SetBanned("Zora", true); err != nil {
		t.Fatal(err)
	}
	_ = bad
	writeEnv(t, a.stream, &Envelope{T: "members"})
	e := a.expect("members")
	var names []string
	for _, m := range e.Members {
		names = append(names, m.Name)
		switch m.Name {
		case "Anna", "Ben":
			if !m.Online {
				t.Errorf("%s sollte online sein", m.Name)
			}
		case "carla":
			if m.Online {
				t.Error("carla ist nicht online")
			}
		}
		if m.Name == "carla" && m.LastSeen == 0 {
			t.Error("carla hat ein Gerät mit last_seen aus der Registrierung")
		}
	}
	if strings.Join(names, ",") != "Anna,Ben,carla" {
		t.Fatalf("Mitglieder: %v", names)
	}

	// Beim Trennen wird last_seen aktualisiert und der Status wechselt
	_ = wa
	b.conn.CloseWithError(0, "")
	deadline := time.Now().Add(3 * time.Second)
	for {
		writeEnv(t, a.stream, &Envelope{T: "members"})
		e = a.expect("members")
		off := false
		for _, m := range e.Members {
			if m.ID == wb.You.ID && !m.Online {
				off = true
			}
		}
		if off {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Ben bleibt online")
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func TestLastSeenOnDisconnect(t *testing.T) {
	srv, _, b, _, wb := twoClients(t)
	// last_seen künstlich alt machen, nach dem Trennen muss es wieder aktuell sein
	if _, err := srv.store.db.Exec("UPDATE devices SET last_seen = 1 WHERE user_id = ?", wb.You.ID); err != nil {
		t.Fatal(err)
	}
	b.conn.CloseWithError(0, "")
	deadline := time.Now().Add(3 * time.Second)
	for {
		ms, _ := srv.store.Members()
		for _, m := range ms {
			if m.ID == wb.You.ID && m.LastSeen > 1000 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("last_seen nicht aktualisiert")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestAvatarMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alt.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	// Alte Datenbank: users ohne die Bildspalten
	if _, err := s.db.Exec("ALTER TABLE users DROP COLUMN avatar"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("ALTER TABLE users DROP COLUMN avatar_ver"); err != nil {
		t.Fatal(err)
	}
	pk := bytes.Repeat([]byte{3}, 32)
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
		t.Fatalf("migration: %v", err)
	}
	if s.AvatarVersion(u.ID) != 0 {
		t.Fatal("alter Nutzer hat ein Bild")
	}
	if _, err := s.SetAvatar(u.ID, []byte("x")); err != nil {
		t.Fatal(err)
	}
}

func TestWebAvatar(t *testing.T) {
	wt := newWebTest(t, nil)
	wt.account("chef@example.org", RoleAdmin, 0)
	wt.account("oma@example.org", RoleUser, PermInvite)
	wt.account("papa@example.org", RoleUser, PermUsersEdit)
	inv, _ := wt.store.CreateInvite(1, time.Hour, "")
	u, _ := wt.store.Register(inv.Code, "Max", bytes.Repeat([]byte{4}, 32))
	jpg, err := prepareAvatar(pngB64(t, 128, 128))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.store.SetAvatar(u.ID, jpg); err != nil {
		t.Fatal(err)
	}
	path := "/users/" + itoa(u.ID) + "/avatar"

	// Ohne Login: umgeleitet
	anon := wt.browser()
	if st, loc, _ := anon.get(path); st != http.StatusSeeOther || loc != "/login" {
		t.Fatalf("ohne Login: %d %s", st, loc)
	}

	// Oma (ohne Recht zum Bearbeiten): sieht das Bild, kann es nicht entfernen
	oma := wt.browser()
	oma.login("oma@example.org")
	resp, err := oma.c.Get(wt.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("Bild: %d %v", resp.StatusCode, resp.Header)
	}
	if _, _, body := oma.get("/users"); !strings.Contains(body, path+"?v=") || strings.Contains(body, "avatar_delete") {
		t.Fatal("Liste: Vorschau erwartet, Entfernen-Knopf nicht")
	}
	tok := oma.csrf()
	if st, _, _ := oma.post("/users/"+itoa(u.ID)+"/avatar_delete", url.Values{"csrf": {tok}}); st != http.StatusForbidden {
		t.Fatalf("Entfernen ohne Recht: %d", st)
	}
	if v := wt.store.AvatarVersion(u.ID); v == 0 {
		t.Fatal("Bild trotz fehlendem Recht entfernt")
	}

	// Papa darf: ohne CSRF nicht, mit CSRF ja
	papa := wt.browser()
	papa.login("papa@example.org")
	if _, _, body := papa.get("/users"); !strings.Contains(body, "avatar_delete") {
		t.Fatal("Entfernen-Knopf fehlt")
	}
	if st, _, _ := papa.post("/users/"+itoa(u.ID)+"/avatar_delete", url.Values{}); st != http.StatusForbidden {
		t.Fatalf("ohne CSRF: %d", st)
	}
	if wt.store.AvatarVersion(u.ID) == 0 {
		t.Fatal("ohne CSRF entfernt")
	}
	if st, loc, _ := papa.post("/users/"+itoa(u.ID)+"/avatar_delete", url.Values{"csrf": {papa.csrf()}}); st != http.StatusSeeOther || !strings.HasPrefix(loc, "/users/") {
		t.Fatalf("Entfernen: %d %s", st, loc)
	}
	if wt.store.AvatarVersion(u.ID) != 0 {
		t.Fatal("Bild nicht entfernt")
	}
	if st, _, _ := papa.get(path); st != http.StatusNotFound {
		t.Fatalf("entferntes Bild: %d", st)
	}
}

func TestAvatarRemovalBroadcast(t *testing.T) {
	srv, a, b, wa, _ := twoClients(t)
	if _, err := srv.store.SetAvatar(wa.You.ID, []byte("x")); err != nil {
		t.Fatal(err)
	}
	srv.hub.AvatarChanged(wa.You.ID, 0)
	for up := b.expect("user_update"); ; up = b.expect("user_update") {
		if up.User.ID == wa.You.ID {
			if up.User.Avatar != 0 {
				t.Fatalf("avatar %d", up.User.Avatar)
			}
			break
		}
	}
	_ = a
}
