package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestInviteNameStore(t *testing.T) {
	srv := startTestServer(t)
	st := srv.store

	inv, err := st.CreateInvite(1, time.Hour, "  Max  ")
	if err != nil || inv.Name != "Max" {
		t.Fatalf("mit Name: %+v %v", inv, err)
	}
	if _, err := st.CreateInvite(1, time.Hour, ""); err != nil {
		t.Fatalf("ohne Name: %v", err)
	}
	if _, err := st.CreateInvite(2, time.Hour, "Max"); !errors.Is(err, ErrInviteNameUses) {
		t.Fatalf("Name bei 2 Personen: %v", err)
	}
	if _, err := st.CreateInvite(1, time.Hour, "x"); !errors.Is(err, ErrInviteName) {
		t.Fatalf("kurzer Name: %v", err)
	}
	invs, _ := st.Invites()
	found := false
	for _, i := range invs {
		if i.Code == inv.Code && i.Name == "Max" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Invites ohne Namen: %+v", invs)
	}

	if n, err := st.InviteName(inv.Code); err != nil || n != "Max" {
		t.Fatalf("InviteName: %q %v", n, err)
	}
	if _, err := st.InviteName("ZZZZ-ZZZZ"); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("ungültig: %v", err)
	}
	exp, _ := st.CreateInvite(1, time.Hour, "Anna")
	if _, err := st.db.Exec("UPDATE invites SET expires = ? WHERE code = ?", time.Now().Add(-time.Minute).Unix(), exp.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InviteName(exp.Code); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("abgelaufen: %v", err)
	}

	// Rückfall: leerer Name übernimmt den Namen der Einladung
	u, err := st.RegisterDevice(inv.Code, "", []byte("schluessel-max-0000000000000000"), "")
	if err != nil || u.Name != "Max" {
		t.Fatalf("Register ohne Name: %+v %v", u, err)
	}
	// Einladung ohne Namen und ohne Eingabe bleibt Fehler
	plain, _ := st.CreateInvite(1, time.Hour, "")
	if _, err := st.RegisterDevice(plain.Code, "", []byte("schluessel-xx-0000000000000000"), ""); !errors.Is(err, ErrNameMissing) {
		t.Fatalf("ohne Name: %v", err)
	}
}

func TestHandshakeInviteInfo(t *testing.T) {
	srv := startTestServer(t)
	named, _ := srv.store.CreateInvite(1, time.Hour, "Max")
	plain, _ := srv.store.CreateInvite(1, time.Hour, "")

	ask := func(code string) *Envelope {
		key := newKey(t)
		conn, st, certHash := dial(t, srv.addr)
		defer conn.CloseWithError(0, "")
		pub := key.Public().(ed25519.PublicKey)
		writeEnv(t, st, &Envelope{T: "hello", V: 1, PubKey: base64.StdEncoding.EncodeToString(pub)})
		ch, err := ReadFrame(st)
		if err != nil || ch.T != "challenge" {
			t.Fatalf("challenge: %+v %v", ch, err)
		}
		nonce, _ := base64.StdEncoding.DecodeString(ch.Nonce)
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, authMessage(nonce, certHash)))
		writeEnv(t, st, &Envelope{T: "invite_info", Sig: sig, Invite: code})
		e, err := ReadFrame(st)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	if e := ask(named.Code); e.T != "invite_info" || e.Name != "Max" {
		t.Fatalf("mit Namen: %+v", e)
	}
	if e := ask(plain.Code); e.T != "invite_info" || e.Name != "" {
		t.Fatalf("ohne Namen: %+v", e)
	}
	if e := ask("ZZZZ-ZZZZ"); e.T != "error" || e.Code != "invite_invalid" {
		t.Fatalf("ungültig: %+v", e)
	}
	// Abfrage verbraucht die Einladung nicht und legt keinen Nutzer an
	if n, err := srv.store.InviteName(named.Code); err != nil || n != "Max" {
		t.Fatalf("Einladung verbraucht: %q %v", n, err)
	}
	// Anmeldung ohne Namen übernimmt den Einladungsnamen
	_, e := login(t, srv.addr, newKey(t), "", named.Code)
	if e.T != "welcome" {
		t.Fatalf("Anmeldung: %+v", e)
	}
}

func TestInviteFormName(t *testing.T) {
	wt := newWebTest(t, nil)
	wt.account("chef@example.org", RoleAdmin, 0)
	b := wt.browser()
	b.login("chef@example.org")
	tok := b.csrf()

	st, loc, _ := b.post("/invites", url.Values{"csrf": {tok}, "uses": {"1"}, "days": {"3"}, "name": {"Max"}})
	if st != http.StatusSeeOther || !strings.HasPrefix(loc, "/invites?new=") || !strings.Contains(loc, "name=Max") {
		t.Fatalf("mit Name: %d %s", st, loc)
	}
	_, _, body := b.get(loc)
	if !strings.Contains(body, "Name (optional)") || !strings.Contains(body, "Max") {
		t.Fatalf("Seite ohne Namen")
	}
	invs, _ := wt.store.Invites()
	if len(invs) != 1 || invs[0].Name != "Max" {
		t.Fatalf("gespeichert: %+v", invs)
	}
	// Name bei mehreren Personen und ungültiger Name
	for _, f := range []url.Values{
		{"csrf": {tok}, "uses": {"2"}, "name": {"Max"}},
		{"csrf": {tok}, "uses": {"1"}, "name": {"x"}},
	} {
		st, loc, _ := b.post("/invites", f)
		if st != http.StatusSeeOther || strings.Contains(loc, "new=") {
			t.Fatalf("Fehler erwartet: %d %s", st, loc)
		}
	}
	if invs, _ := wt.store.Invites(); len(invs) != 1 {
		t.Fatalf("zu viele Einladungen: %+v", invs)
	}
}
