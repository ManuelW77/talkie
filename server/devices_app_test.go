package main

import (
	"bytes"
	"testing"
	"time"
)

// appSession baut eine Sitzung ohne echte Verbindung; Antworten landen in s.out.
func appSession(t *testing.T, wt *webTest, u *DBUser, pk []byte) *Session {
	t.Helper()
	return &Session{hub: wt.hub, userID: u.ID, name: u.Name, pubkey: pk,
		out: make(chan []byte, 16), done: make(chan struct{})}
}

func nextEnv(t *testing.T, s *Session) *Envelope {
	t.Helper()
	select {
	case f := <-s.out:
		e, err := ReadFrame(bytes.NewReader(f))
		if err != nil {
			t.Fatal(err)
		}
		return e
	default:
		t.Fatal("keine Antwort")
		return nil
	}
}

func TestAppDevices(t *testing.T) {
	wt := newWebTest(t, nil)
	inv, _ := wt.store.CreateInvite(2, time.Hour, "")
	pk1 := bytes.Repeat([]byte{1}, 32)
	u, err := wt.store.Register(inv.Code, "Mia", pk1)
	if err != nil {
		t.Fatal(err)
	}
	other, err := wt.store.Register(inv.Code, "Ben", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := appSession(t, wt, u, pk1)

	// Mit Namen angeforderter Code taucht in der Liste auf, der Name wird zum Gerätenamen
	wt.hub.SendDeviceCode(s, "Mein iPhone")
	if e := nextEnv(t, s); e.T != "device_code" || e.Code == "" {
		t.Fatalf("device_code: %+v", e)
	}
	e := nextEnv(t, s)
	if e.T != "devices" || len(e.Devices) != 1 || !e.Devices[0].This || e.Code == "" || e.Expires == 0 || e.Name != "Mein iPhone" {
		t.Fatalf("devices: %+v", e)
	}
	code := e.Code
	pk2 := bytes.Repeat([]byte{2}, 32)
	if _, err := wt.store.RegisterDevice(code, "", pk2, "iOS"); err != nil {
		t.Fatal(err)
	}
	wt.hub.SendDevices(s)
	e = nextEnv(t, s)
	if len(e.Devices) != 2 || e.Code != "" {
		t.Fatalf("nach Einlösen: %+v", e)
	}
	var second, thisID int64
	for _, d := range e.Devices {
		if d.This {
			thisID = d.ID
		} else {
			second = d.ID
			if d.Label != "Mein iPhone" {
				t.Fatalf("Label: %q", d.Label)
			}
		}
	}

	// Das eigene (aktuelle) Gerät ist nicht entfernbar
	wt.hub.DeleteOwnDevice(s, thisID)
	if e := nextEnv(t, s); e.T != "error" || e.Code != "forbidden" {
		t.Fatalf("eigenes Gerät: %+v", e)
	}
	// Fremdes Gerät eines anderen Nutzers: weder entfernbar noch umbenennbar
	od, _ := wt.store.Devices(other.ID)
	wt.hub.DeleteOwnDevice(s, od[0].ID)
	if e := nextEnv(t, s); e.Code != "not_found" {
		t.Fatalf("fremdes Gerät: %+v", e)
	}
	wt.hub.RenameOwnDevice(s, od[0].ID, "Hack")
	if e := nextEnv(t, s); e.Code != "not_found" {
		t.Fatalf("fremdes umbenennen: %+v", e)
	}
	if d, _ := wt.store.Devices(other.ID); len(d) != 1 || d[0].Label == "Hack" {
		t.Fatal("fremdes Gerät verändert")
	}
	// Umbenennen (auch das aktuelle), zu lange Namen werden abgelehnt
	wt.hub.RenameOwnDevice(s, thisID, "Mein Mac")
	e = nextEnv(t, s)
	found := false
	for _, d := range e.Devices {
		if d.ID == thisID && d.Label == "Mein Mac" {
			found = true
		}
	}
	if !found {
		t.Fatalf("umbenannt: %+v", e)
	}
	wt.hub.RenameOwnDevice(s, thisID, string(bytes.Repeat([]byte("a"), 41)))
	if e := nextEnv(t, s); e.Code != "bad_name" {
		t.Fatalf("langer Name: %+v", e)
	}
	// Anderes Gerät entfernen
	wt.hub.DeleteOwnDevice(s, second)
	if e := nextEnv(t, s); e.T != "devices" || len(e.Devices) != 1 {
		t.Fatalf("nach Entfernen: %+v", e)
	}
	// Das letzte Gerät bleibt
	wt.hub.DeleteOwnDevice(s, thisID)
	if e := nextEnv(t, s); e.Code != "forbidden" {
		t.Fatalf("letztes: %+v", e)
	}

	// Code löschen wirkt, ohne fremde Codes anzufassen
	wt.hub.SendDeviceCode(s, "")
	nextEnv(t, s)
	nextEnv(t, s)
	oc, _ := wt.store.CreateDeviceCode(other.ID, time.Hour, "")
	wt.hub.DeleteOwnDeviceCode(s)
	if e := nextEnv(t, s); e.T != "devices" || e.Code != "" {
		t.Fatalf("Code-Löschen: %+v", e)
	}
	if wt.store.DeviceCode(u.ID) != nil || wt.store.DeviceCode(other.ID) == nil || wt.store.DeviceCode(other.ID).Code != oc.Code {
		t.Fatal("Code-Löschen wirkt falsch")
	}
	// Normale Einladungen behalten ihre Notiz und bleiben in der Liste der Einladungen
	wt.store.CreateInvite(1, time.Hour, "für Oma")
	if invs, _ := wt.store.Invites(); len(invs) != 1 || invs[0].Name != "für Oma" {
		t.Fatalf("Einladungen: %+v", invs)
	}
}
