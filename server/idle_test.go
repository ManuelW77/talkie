package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock ist eine Uhr, die nur auf Befehl weiterläuft.
type fakeClock struct{ ms atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.ms.Store(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).UnixMilli())
	return c
}
func (c *fakeClock) Now() time.Time          { return time.UnixMilli(c.ms.Load()) }
func (c *fakeClock) Advance(d time.Duration) { c.ms.Add(d.Milliseconds()) }

func idleServer(t *testing.T, minutes int) (*testServer, *fakeClock) {
	t.Helper()
	srv := startTestServer(t)
	clk := newFakeClock()
	srv.hub.now = clk.Now
	if err := srv.store.SetIdleMinutes(minutes); err != nil {
		t.Fatal(err)
	}
	return srv, clk
}

func idleLogin(t *testing.T, srv *testServer, name string) *testClient {
	t.Helper()
	inv, err := srv.store.CreateInvite(1, time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	c, w := login(t, srv.addr, newKey(t), name, inv.Code)
	if w.T != "welcome" {
		t.Fatalf("welcome: %+v", w)
	}
	return c
}

// noFrame prüft, dass kurz keine Nachricht vom Typ typ ankommt.
func noFrame(t *testing.T, c *testClient, typ string) {
	t.Helper()
	timeout := time.After(300 * time.Millisecond)
	for {
		select {
		case e, ok := <-c.frames:
			if !ok {
				return
			}
			if e.T == typ {
				t.Fatalf("unerwartet %q: %+v", typ, e)
			}
		case <-timeout:
			return
		}
	}
}

func TestIdleSettingDefaultAndRange(t *testing.T) {
	srv := startTestServer(t)
	if n := srv.store.IdleMinutes(); n != 60 {
		t.Fatalf("Standard: %d", n)
	}
	for _, bad := range []int{-1, 1, 9, 1441} {
		if srv.store.SetIdleMinutes(bad) == nil {
			t.Fatalf("%d sollte abgelehnt werden", bad)
		}
	}
	for _, ok := range []int{0, 10, 90, 1440} {
		if err := srv.store.SetIdleMinutes(ok); err != nil || srv.store.IdleMinutes() != ok {
			t.Fatalf("%d: err=%v got=%d", ok, err, srv.store.IdleMinutes())
		}
	}
}

func TestIdleWarningThenKick(t *testing.T) {
	srv, clk := idleServer(t, 60)
	c := idleLogin(t, srv, "Anna")

	clk.Advance(54 * time.Minute)
	srv.hub.checkIdle()
	noFrame(t, c, "idle_warning")

	clk.Advance(1*time.Minute + 30*time.Second) // 55:30 → noch 4:30
	srv.hub.checkIdle()
	w := c.expect("idle_warning")
	if w.Secs != 270 {
		t.Fatalf("Sekunden: %d", w.Secs)
	}
	// nur einmal warnen
	srv.hub.checkIdle()
	noFrame(t, c, "idle_warning")

	clk.Advance(5 * time.Minute)
	srv.hub.checkIdle()
	k := c.expect("kicked")
	if !strings.Contains(k.Reason, "60 Minuten nicht aktiv") {
		t.Fatalf("Grund: %q", k.Reason)
	}
}

func TestIdleActivityResets(t *testing.T) {
	srv, clk := idleServer(t, 30)
	c := idleLogin(t, srv, "Ben")

	clk.Advance(26 * time.Minute)
	srv.hub.checkIdle()
	c.expect("idle_warning")

	// Bestätigung setzt zurück
	writeEnv(t, c.stream, &Envelope{T: "still_here"})
	time.Sleep(200 * time.Millisecond)
	clk.Advance(10 * time.Minute)
	srv.hub.checkIdle()
	noFrame(t, c, "kicked")
	noFrame(t, c, "idle_warning")

	// danach darf erneut gewarnt werden
	clk.Advance(16 * time.Minute)
	srv.hub.checkIdle()
	c.expect("idle_warning")

	// eine andere Aktion (Chat) setzt auch zurück
	writeEnv(t, c.stream, &Envelope{T: "chat", Text: "hi"})
	time.Sleep(200 * time.Millisecond)
	clk.Advance(20 * time.Minute)
	srv.hub.checkIdle()
	noFrame(t, c, "kicked")
}

func TestIdleVoiceCountsOnlyRealSpeech(t *testing.T) {
	srv, clk := idleServer(t, 30)
	c := idleLogin(t, srv, "Mia")

	clk.Advance(20 * time.Minute)
	// DTX-/Komfortrauschen-Paket (1 Byte Opus): zählt nicht
	_ = c.conn.SendDatagram([]byte{DatagramVoice, 0, 1, 0xF8})
	time.Sleep(200 * time.Millisecond)
	clk.Advance(6 * time.Minute)
	srv.hub.checkIdle()
	c.expect("idle_warning")

	// echte Sprache zählt
	_ = c.conn.SendDatagram([]byte{DatagramVoice, 0, 2, 1, 2, 3, 4, 5})
	time.Sleep(200 * time.Millisecond)
	clk.Advance(20 * time.Minute)
	srv.hub.checkIdle()
	noFrame(t, c, "kicked")
}

func TestIdleOffAndLiveChange(t *testing.T) {
	srv, clk := idleServer(t, 0)
	c := idleLogin(t, srv, "Leo")
	clk.Advance(48 * time.Hour)
	srv.hub.checkIdle()
	noFrame(t, c, "kicked")
	noFrame(t, c, "idle_warning")

	// Einstellung wirkt sofort
	if err := srv.store.SetIdleMinutes(10); err != nil {
		t.Fatal(err)
	}
	srv.hub.checkIdle()
	c.expect("kicked")
}

func TestIdleKickEvenWithOthersInRoom(t *testing.T) {
	srv, clk := idleServer(t, 10)
	a := idleLogin(t, srv, "Anna")
	b := idleLogin(t, srv, "Ben")
	clk.Advance(9 * time.Minute)
	writeEnv(t, b.stream, &Envelope{T: "still_here"})
	time.Sleep(200 * time.Millisecond)
	clk.Advance(2 * time.Minute)
	srv.hub.checkIdle()
	a.expect("kicked")
	noFrame(t, b, "kicked")
}

func TestIdleWatchRuns(t *testing.T) {
	srv, _ := idleServer(t, 10)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.hub.RunIdleWatch(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunIdleWatch endet nicht")
	}
}

func TestWebIdleSetting(t *testing.T) {
	wt := newWebTest(t, nil)
	wt.account("chef@x.de", RoleAdmin, 0)
	wt.account("helfer@x.de", RoleUser, PermRooms|PermInvite)

	h := wt.browser()
	h.login("helfer@x.de")
	if _, _, body := h.get("/rooms"); strings.Contains(body, "Trennen nach Inaktivität") {
		t.Fatal("Nicht-Admin sieht das Feld")
	}
	if st, _, _ := h.post("/settings/idle", url.Values{"csrf": {h.csrf()}, "minutes": {"20"}}); st != http.StatusForbidden {
		t.Fatalf("Nicht-Admin durfte speichern: %d", st)
	}

	a := wt.browser()
	a.login("chef@x.de")
	if _, _, body := a.get("/rooms"); !strings.Contains(body, "Trennen nach Inaktivität (Minuten, 0 = aus)") || !strings.Contains(body, `value="60"`) {
		t.Fatal("Admin: Feld oder Standardwert fehlt")
	}
	// ohne CSRF-Token
	if st, _, _ := a.post("/settings/idle", url.Values{"minutes": {"20"}}); st == http.StatusSeeOther || wt.store.IdleMinutes() != 60 {
		t.Fatalf("ohne CSRF gespeichert: %d, %d", st, wt.store.IdleMinutes())
	}
	// ungültige Werte
	for _, bad := range []string{"5", "1441", "-3", "abc", ""} {
		a.post("/settings/idle", url.Values{"csrf": {a.csrf()}, "minutes": {bad}})
		if n := wt.store.IdleMinutes(); n != 60 {
			t.Fatalf("%q gespeichert: %d", bad, n)
		}
	}
	// gültig
	a.post("/settings/idle", url.Values{"csrf": {a.csrf()}, "minutes": {"20"}})
	if n := wt.store.IdleMinutes(); n != 20 {
		t.Fatalf("gespeichert: %d", n)
	}
	a.post("/settings/idle", url.Values{"csrf": {a.csrf()}, "minutes": {"0"}})
	if n := wt.store.IdleMinutes(); n != 0 {
		t.Fatalf("aus: %d", n)
	}
	if log, _ := wt.store.AuditLog(5); len(log) < 2 || !strings.Contains(log[0].Action, "Inaktivität") {
		t.Fatalf("Protokoll: %+v", log)
	}
}
