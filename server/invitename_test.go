package main

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestInviteNoteStore(t *testing.T) {
	srv := startTestServer(t)
	st := srv.store

	inv, err := st.CreateInvite(1, time.Hour, "  Max \t (Klasse   5b) ")
	if err != nil || inv.Name != "Max (Klasse 5b)" {
		t.Fatalf("mit Notiz: %+v %v", inv, err)
	}
	if _, err := st.CreateInvite(1, time.Hour, ""); err != nil {
		t.Fatalf("ohne Notiz: %v", err)
	}
	if multi, err := st.CreateInvite(5, time.Hour, "Klasse 5b"); err != nil || multi.Name != "Klasse 5b" {
		t.Fatalf("mehrere Personen: %+v %v", multi, err)
	}
	if _, err := st.CreateInvite(1, time.Hour, strings.Repeat("ä", 40)); err != nil {
		t.Fatalf("genau 40 Zeichen: %v", err)
	}
	if _, err := st.CreateInvite(1, time.Hour, strings.Repeat("ä", 41)); !errors.Is(err, ErrInviteName) {
		t.Fatalf("zu lang: %v", err)
	}
	if _, err := st.CreateInvite(1, time.Hour, "Max\x07"); !errors.Is(err, ErrInviteName) {
		t.Fatalf("Steuerzeichen: %v", err)
	}
	invs, _ := st.Invites()
	found := false
	for _, i := range invs {
		if i.Code == inv.Code && i.Name == "Max (Klasse 5b)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Invites ohne Notiz: %+v", invs)
	}

	// Die Notiz wird nie zum Namen: ohne eigene Eingabe bleibt es ein Fehler
	if _, err := st.RegisterDevice(inv.Code, "", []byte("schluessel-max-0000000000000000"), ""); !errors.Is(err, ErrNameMissing) {
		t.Fatalf("leerer Name bei Einladung mit Notiz: %v", err)
	}
}

func TestInviteFormNote(t *testing.T) {
	wt := newWebTest(t, nil)
	wt.account("chef@example.org", RoleAdmin, 0)
	b := wt.browser()
	b.login("chef@example.org")
	tok := b.csrf()

	st, loc, _ := b.post("/invites", url.Values{"csrf": {tok}, "uses": {"1"}, "days": {"3"}, "name": {"Max"}})
	if st != http.StatusSeeOther || !strings.HasPrefix(loc, "/invites?new=") || !strings.Contains(loc, "name=Max") {
		t.Fatalf("mit Notiz: %d %s", st, loc)
	}
	_, _, body := b.get(loc)
	if !strings.Contains(body, "Für wen? (optional)") || !strings.Contains(body, "Für Max:") ||
		!strings.Contains(body, "Name ausdenken, Code eingeben, fertig.") {
		t.Fatalf("Seite ohne Notiz")
	}
	invs, _ := wt.store.Invites()
	if len(invs) != 1 || invs[0].Name != "Max" {
		t.Fatalf("gespeichert: %+v", invs)
	}
	// bei mehreren Personen erlaubt
	st, loc, _ = b.post("/invites", url.Values{"csrf": {tok}, "uses": {"3"}, "name": {"Klasse 5b"}})
	if st != http.StatusSeeOther || !strings.Contains(loc, "new=") {
		t.Fatalf("mehrere Personen: %d %s", st, loc)
	}
	// zu lang
	st, loc, _ = b.post("/invites", url.Values{"csrf": {tok}, "uses": {"1"}, "name": {strings.Repeat("x", 41)}})
	if st != http.StatusSeeOther || strings.Contains(loc, "new=") {
		t.Fatalf("Fehler erwartet: %d %s", st, loc)
	}
	if invs, _ := wt.store.Invites(); len(invs) != 2 {
		t.Fatalf("Anzahl Einladungen: %+v", invs)
	}
}
