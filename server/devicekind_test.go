package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeviceKindRenameAndSet(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	inv, _ := s.CreateInvite(1, time.Hour, "")
	pk := bytes.Repeat([]byte{7}, 32)
	u, err := s.RegisterDeviceKind(inv.Code, "Mia", pk, "Mac", "Mac")
	if err != nil {
		t.Fatal(err)
	}
	devs, _ := s.Devices(u.ID)
	if len(devs) != 1 || devs[0].Kind != "Mac" || devs[0].Label != "Mac" {
		t.Fatalf("geräte: %+v", devs)
	}
	if err := s.RenameDevice(u.ID, devs[0].ID, "Mias Laptop"); err != nil {
		t.Fatal(err)
	}
	d, _ := s.DeviceByPubKey(pk)
	if d.Kind != "Mac" || d.Label != "Mias Laptop" {
		t.Fatalf("nach Umbenennen: %+v", d)
	}
	// Anmeldung mit device setzt den Typ, der Name bleibt
	if err := s.SetDeviceKind(pk, "Windows"); err != nil {
		t.Fatal(err)
	}
	d, _ = s.DeviceByPubKey(pk)
	if d.Kind != "Windows" || d.Label != "Mias Laptop" {
		t.Fatalf("nach SetDeviceKind: %+v", d)
	}
}

func TestDeviceKindMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alt.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("ALTER TABLE devices DROP COLUMN kind"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO users(name, pubkey, created) VALUES('Alt', ?, 1)", bytes.Repeat([]byte{1}, 32)); err != nil {
		t.Fatal(err)
	}
	for i, l := range []string{"iPhone/iPad", "Mias Laptop"} {
		if _, err := s.db.Exec("INSERT INTO devices(user_id, pubkey, label, created) VALUES(1, ?, ?, 1)", bytes.Repeat([]byte{byte(10 + i)}, 32), l); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.DeviceByPubKey(bytes.Repeat([]byte{10}, 32))
	b, _ := s.DeviceByPubKey(bytes.Repeat([]byte{11}, 32))
	if a == nil || a.Kind != "iPhone/iPad" || b == nil || b.Kind != "" || b.Label != "Mias Laptop" {
		t.Fatalf("backfill: %+v %+v", a, b)
	}
}

func TestWebUserShowsKindAndName(t *testing.T) {
	wt := newWebTest(t, nil)
	wt.account("chef@x.de", RoleAdmin, 0)
	inv, _ := wt.store.CreateInvite(1, time.Hour, "")
	pk := bytes.Repeat([]byte{5}, 32)
	u, err := wt.store.RegisterDeviceKind(inv.Code, "Mia", pk, "Mac", "Mac")
	if err != nil {
		t.Fatal(err)
	}
	devs, _ := wt.store.Devices(u.ID)
	wt.store.RenameDevice(u.ID, devs[0].ID, "Küchenrechner")
	b := wt.browser()
	b.login("chef@x.de")
	_, _, body := b.get("/users/" + itoa(u.ID))
	if !strings.Contains(body, "<th>Name</th>") || !strings.Contains(body, "<td>Mac</td>") || !strings.Contains(body, "Küchenrechner") {
		t.Fatal("Typ oder Name fehlt auf der Benutzerseite")
	}
}

func TestAuthSetsDeviceKind(t *testing.T) {
	srv := startTestServer(t)
	inv, _ := srv.store.CreateInvite(1, time.Hour, "")
	key := newKey(t)
	pk := key.Public().(ed25519.PublicKey)
	if _, err := srv.store.RegisterDeviceKind(inv.Code, "Lia", pk, "", ""); err != nil {
		t.Fatal(err)
	}
	// alte App: auth ohne device ändert nichts
	send := func(device string) {
		conn, st, certHash := dial(t, srv.addr)
		defer conn.CloseWithError(0, "")
		writeEnv(t, st, &Envelope{T: "hello", V: 1, PubKey: base64.StdEncoding.EncodeToString(pk)})
		ch, _ := ReadFrame(st)
		nonce, _ := base64.StdEncoding.DecodeString(ch.Nonce)
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, authMessage(nonce, certHash)))
		writeEnv(t, st, &Envelope{T: "auth", Sig: sig, Device: device})
		if w, _ := ReadFrame(st); w.T != "welcome" {
			t.Fatalf("%+v", w)
		}
	}
	send("")
	if d, _ := srv.store.DeviceByPubKey(pk); d.Kind != "" {
		t.Fatalf("kind ohne device: %q", d.Kind)
	}
	send("Linux")
	if d, _ := srv.store.DeviceByPubKey(pk); d.Kind != "Linux" {
		t.Fatalf("kind: %q", d.Kind)
	}
}
