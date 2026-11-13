package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

type fakeMailer struct {
	mu   sync.Mutex
	sent []string // "to|subject|body"
	html []string // HTML-Fassung, parallel zu sent
}

func (f *fakeMailer) Send(to, subject, body, html string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, to+"|"+subject+"|"+body)
	f.html = append(f.html, html)
	return nil
}

func (f *fakeMailer) last(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second) // Login-Mails gehen asynchron raus
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := len(f.sent)
		var m string
		if n > 0 {
			m = f.sent[n-1]
		}
		f.mu.Unlock()
		if n > 0 {
			return m
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("keine Mail verschickt")
	return ""
}

func (f *fakeMailer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

type webTest struct {
	t     *testing.T
	srv   *httptest.Server
	store *Store
	hub   *Hub
	mail  *fakeMailer
	app   *webApp
}

func newWebTest(t *testing.T, oidcCfg *OIDCConfig) *webTest {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	hub, err := NewHub(store, make([]byte, 32), 10)
	if err != nil {
		t.Fatal(err)
	}
	var h http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)
	m := &fakeMailer{}
	app, err := newWebApp(hub, store, WebConfig{PublicURL: srv.URL, Mailer: m, OIDC: oidcCfg, ServerAddr: "talkie.test:7777"})
	if err != nil {
		t.Fatal(err)
	}
	h = app.handler()
	return &webTest{t: t, srv: srv, store: store, hub: hub, mail: m, app: app}
}

func (wt *webTest) account(email, role string, perms Perm) *Account {
	wt.t.Helper()
	a, err := wt.store.AddAccount(Account{Email: email, Name: strings.Split(email, "@")[0], Role: role, Perms: perms})
	if err != nil {
		wt.t.Fatal(err)
	}
	return a
}

// browser ist ein Client mit Cookies, der Weiterleitungen nicht folgt.
type browser struct {
	wt *webTest
	c  *http.Client
}

func (wt *webTest) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{wt, &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
}

func (b *browser) get(path string) (int, string, string) {
	b.wt.t.Helper()
	resp, err := b.c.Get(b.wt.srv.URL + path)
	if err != nil {
		b.wt.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location"), string(body)
}

func (b *browser) post(path string, form url.Values) (int, string, string) {
	b.wt.t.Helper()
	req, _ := http.NewRequest("POST", b.wt.srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", b.wt.srv.URL)
	resp, err := b.c.Do(req)
	if err != nil {
		b.wt.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location"), string(body)
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func (b *browser) csrf() string {
	b.wt.t.Helper()
	_, _, body := b.get("/")
	m := csrfRe.FindStringSubmatch(body)
	if m == nil {
		b.wt.t.Fatal("kein CSRF-Token auf der Seite")
	}
	return m[1]
}

var codeRe = regexp.MustCompile(`(\d{4}) (\d{4})`)
var linkRe = regexp.MustCompile(`/login/link\?t=([A-Za-z0-9_-]+)`)

// login meldet per Mail-Code an.
func (b *browser) login(email string) {
	b.wt.t.Helper()
	before := b.wt.mail.count()
	if st, loc, _ := b.post("/login", url.Values{"email": {email}}); st != http.StatusSeeOther || loc != "/login/code" {
		b.wt.t.Fatalf("login: %d %s", st, loc)
	}
	for i := 0; b.wt.mail.count() == before; i++ {
		if i > 200 {
			b.wt.t.Fatal("keine Login-Mail")
		}
		time.Sleep(10 * time.Millisecond)
	}
	m := codeRe.FindStringSubmatch(b.wt.mail.last(b.wt.t))
	if st, loc, body := b.post("/login/code", url.Values{"code": {m[1] + " " + m[2]}}); st != http.StatusSeeOther || loc != "/" {
		b.wt.t.Fatalf("code: %d %s %s", st, loc, body)
	}
}

func TestWebMailLoginAndPages(t *testing.T) {
	wt := newWebTest(t, nil)
	wt.account("chef@example.org", RoleAdmin, 0)
	b := wt.browser()

	if st, loc, _ := b.get("/"); st != http.StatusSeeOther || loc != "/login" {
		t.Fatalf("ohne Login: %d %s", st, loc)
	}
	b.login("Chef@Example.org ") // Groß/klein und Leerzeichen egal
	if !strings.Contains(wt.mail.last(t), "/login/link?t=") {
		t.Fatal("Mail ohne Link")
	}
	// Alle Seiten rendern ohne Fehler
	for _, p := range []string{"/", "/users", "/invites", "/rooms", "/chat", "/accounts", "/accounts/new", "/accounts/1", "/audit"} {
		st, _, body := b.get(p)
		if st != http.StatusOK || strings.Contains(body, "schiefgegangen") {
			t.Fatalf("%s: %d", p, st)
		}
	}
	// Abmelden
	tok := b.csrf()
	b.post("/logout", url.Values{"csrf": {tok}})
	if st, _, _ := b.get("/"); st != http.StatusSeeOther {
		t.Fatal("nach Abmelden noch angemeldet")
	}
}

func TestWebUnknownEmailNoMail(t *testing.T) {
	wt := newWebTest(t, nil)
	b := wt.browser()
	st, loc, _ := b.post("/login", url.Values{"email": {"fremd@example.org"}})
	if st != http.StatusSeeOther || loc != "/login/code" {
		t.Fatalf("Antwort verrät, dass es das Konto nicht gibt: %d %s", st, loc)
	}
	time.Sleep(100 * time.Millisecond)
	if wt.mail.count() != 0 {
		t.Fatal("Mail an unbekannte Adresse")
	}
	if _, _, body := b.post("/login/code", url.Values{"code": {"12345678"}}); !strings.Contains(body, "stimmt nicht") {
		t.Fatal("Code ohne Konto angenommen")
	}
}

func TestWebCodeAttemptsLimited(t *testing.T) {
	wt := newWebTest(t, nil)
	wt.account("a@example.org", RoleAdmin, 0)
	b := wt.browser()
	b.post("/login", url.Values{"email": {"a@example.org"}})
	m := codeRe.FindStringSubmatch(wt.mail.last(t))
	right := m[1] + m[2]
	wrong := "00000000"
	if right == wrong {
		wrong = "11111111"
	}
	for i := 0; i < loginMaxAttempts; i++ {
		b.post("/login/code", url.Values{"code": {wrong}})
	}
	if _, loc, _ := b.post("/login/code", url.Values{"code": {right}}); loc == "/" {
		t.Fatal("nach 5 Fehlversuchen trotzdem angemeldet")
	}
	// Der Code gehört zum Browser, der ihn angefordert hat
	b2 := wt.browser()
	b2.post("/login", url.Values{"email": {"a@example.org"}})
	for wt.mail.count() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	m2 := codeRe.FindStringSubmatch(wt.mail.last(t))
	other := wt.browser()
	if _, loc, _ := other.post("/login/code", url.Values{"code": {m2[1] + m2[2]}}); loc == "/" {
		t.Fatal("Code in fremdem Browser angenommen")
	}
	other.post("/login", url.Values{"email": {"x@example.org"}}) // hat jetzt ein eigenes Cookie
	if _, loc, _ := other.post("/login/code", url.Values{"code": {m2[1] + m2[2]}}); loc == "/" {
		t.Fatal("Code mit fremdem Cookie angenommen")
	}
	if _, loc, _ := b2.post("/login/code", url.Values{"code": {m2[1] + m2[2]}}); loc != "/" {
		t.Fatal("richtiger Browser wurde abgewiesen")
	}
}

func TestWebMailLimitKeepsCode(t *testing.T) {
	wt := newWebTest(t, nil)
	wt.account("a@example.org", RoleAdmin, 0)
	b := wt.browser()
	for i := 0; i < loginMailsPerWindow; i++ {
		b.post("/login", url.Values{"email": {"a@example.org"}})
	}
	for wt.mail.count() < loginMailsPerWindow {
		time.Sleep(10 * time.Millisecond)
	}
	last := codeRe.FindStringSubmatch(wt.mail.last(t))
	// Über dem Limit: keine Mail, aber der letzte Code bleibt gültig
	b.post("/login", url.Values{"email": {"a@example.org"}})
	time.Sleep(100 * time.Millisecond)
	if wt.mail.count() != loginMailsPerWindow {
		t.Fatal("Limit greift nicht")
	}
	if _, loc, _ := b.post("/login/code", url.Values{"code": {last[1] + last[2]}}); loc != "/" {
		t.Fatal("letzter Code nach erneuter Anforderung ungültig")
	}
}

func TestWebLinkLogin(t *testing.T) {
	wt := newWebTest(t, nil)
	wt.account("a@example.org", RoleUser, 0)
	b := wt.browser()
	b.post("/login", url.Values{"email": {"a@example.org"}})
	link := linkRe.FindStringSubmatch(wt.mail.last(t))[1]

	// Link in einem anderen Browser (z. B. Handy): GET verbraucht nichts
	phone := wt.browser()
	if st, _, body := phone.get("/login/link?t=" + link); st != http.StatusOK || !strings.Contains(body, "Jetzt anmelden") {
		t.Fatalf("Linkseite: %d", st)
	}
	// Abgeschottetes Fenster / fremde Seite („Origin: null“): kein Login (Login-CSRF),
	// Link bleibt aber gültig und die Login-Seite erklärt, was zu tun ist
	sandbox := wt.browser()
	req, _ := http.NewRequest("POST", wt.srv.URL+"/login/link", strings.NewReader(url.Values{"t": {link}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "null")
	resp, err := sandbox.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Location") != "/login" || !strings.Contains(sandbox.flash(), "Adresszeile") {
		t.Fatal("Link-Login mit Origin null nicht sauber abgelehnt")
	}
	if st, loc, _ := phone.post("/login/link", url.Values{"t": {link}}); st != http.StatusSeeOther || loc != "/" {
		t.Fatalf("Link-Login: %d %s", st, loc)
	}
	if st, _, _ := phone.get("/"); st != http.StatusOK {
		t.Fatal("nach Link nicht angemeldet")
	}
	again := wt.browser()
	if _, loc, _ := again.post("/login/link", url.Values{"t": {link}}); loc != "/login" || !strings.Contains(again.flash(), "abgelaufen") {
		t.Fatal("Link zweimal benutzbar")
	}
}

func TestWebPermissions(t *testing.T) {
	wt := newWebTest(t, nil)
	wt.account("chef@example.org", RoleAdmin, 0)
	wt.account("oma@example.org", RoleUser, PermInvite)
	b := wt.browser()
	b.login("oma@example.org")
	tok := b.csrf()

	if st, _, _ := b.get("/rooms"); st != http.StatusOK {
		t.Fatal("Räume ansehen sollte gehen")
	}
	if st, _, _ := b.post("/rooms", url.Values{"csrf": {tok}, "name": {"Hacker"}}); st != http.StatusForbidden {
		t.Fatalf("Raum anlegen ohne Recht: %d", st)
	}
	for _, p := range []string{"/accounts", "/audit", "/chat"} {
		if st, _, _ := b.get(p); st != http.StatusForbidden {
			t.Fatalf("%s ohne Recht: %d", p, st)
		}
	}
	if st, loc, _ := b.post("/invites", url.Values{"csrf": {tok}, "uses": {"2"}, "days": {"3"}, "email": {"eltern@example.org"}}); st != http.StatusSeeOther || !strings.HasPrefix(loc, "/invites?new=") {
		t.Fatalf("Einladung: %d %s", st, loc)
	}
	if m := wt.mail.last(t); !strings.HasPrefix(m, "eltern@example.org|") || !strings.Contains(m, "talkie.test:7777") {
		t.Fatalf("Einladungsmail: %s", m)
	}
	// Talkie-Nutzer löschen ohne Recht
	inv, _ := wt.store.CreateInvite(1, time.Hour)
	u, _ := wt.store.Register(inv.Code, "Max", []byte("schluessel-max"))
	if st, _, _ := b.post("/users/"+itoa(u.ID)+"/delete", url.Values{"csrf": {tok}}); st != http.StatusForbidden {
		t.Fatalf("Nutzer löschen ohne Recht: %d", st)
	}
	// Ohne CSRF-Token
	if st, _, _ := b.post("/invites", url.Values{"uses": {"1"}}); st != http.StatusForbidden {
		t.Fatalf("ohne CSRF: %d", st)
	}
	// Fremde Seite
	req, _ := http.NewRequest("POST", wt.srv.URL+"/invites", strings.NewReader("csrf="+tok))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://boese.example")
	resp, _ := b.c.Do(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("fremder Origin: %d", resp.StatusCode)
	}
	resp.Body.Close()
	// Die Seite muss Browsern erlauben, den eigenen Origin bei Formularen mitzuschicken
	// (bei no-referrer kommt „Origin: null“ und jedes Formular würde abgelehnt).
	if p := resp.Header.Get("Referrer-Policy"); p == "no-referrer" || p == "" {
		t.Fatalf("Referrer-Policy %q bricht Formulare", p)
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func TestWebAdminManages(t *testing.T) {
	wt := newWebTest(t, nil)
	chef := wt.account("chef@example.org", RoleAdmin, 0)
	b := wt.browser()
	b.login("chef@example.org")
	tok := b.csrf()

	// Letzter Admin kann sich nicht herabstufen
	b.post("/accounts/"+itoa(chef.ID), url.Values{"csrf": {tok}, "email": {"chef@example.org"}, "name": {"Chef"}, "role": {"user"}})
	if a, _ := wt.store.AccountByID(chef.ID); !a.IsAdmin() {
		t.Fatal("letzter Admin herabgestuft")
	}
	// Konto anlegen mit Rechten
	b.post("/accounts/new", url.Values{"csrf": {tok}, "email": {"papa@example.org"}, "name": {"Papa"}, "role": {"user"},
		"perm_rooms": {"1"}, "perm_chat": {"1"}})
	papa, err := wt.store.AccountByEmail("papa@example.org")
	if err != nil || papa.Perms != PermRooms|PermChat || papa.IsAdmin() {
		t.Fatalf("Konto: %+v %v", papa, err)
	}
	// Räume: anlegen, umbenennen, verschieben, löschen
	b.post("/rooms", url.Values{"csrf": {tok}, "name": {"Minecraft"}})
	r, err := wt.store.RoomByName("Minecraft")
	if err != nil {
		t.Fatal("Raum nicht angelegt")
	}
	b.post("/rooms/"+itoa(r.ID)+"/rename", url.Values{"csrf": {tok}, "name": {"Fortnite"}})
	b.post("/rooms/"+itoa(r.ID)+"/up", url.Values{"csrf": {tok}})
	rooms, _ := wt.store.Rooms()
	if rooms[len(rooms)-2].Name != "Fortnite" {
		t.Fatalf("Reihenfolge: %+v", rooms)
	}
	b.post("/rooms/"+itoa(r.ID)+"/delete", url.Values{"csrf": {tok}})
	if _, err := wt.store.RoomByID(r.ID); err == nil {
		t.Fatal("Raum nicht gelöscht")
	}
	// Talkie-Nutzer umbenennen, sperren, löschen
	inv, _ := wt.store.CreateInvite(1, time.Hour)
	u, _ := wt.store.Register(inv.Code, "Max", []byte("schluessel-max"))
	b.post("/users/"+itoa(u.ID)+"/rename", url.Values{"csrf": {tok}, "name": {"Maxi"}})
	b.post("/users/"+itoa(u.ID)+"/ban", url.Values{"csrf": {tok}})
	if nu, _ := wt.store.UserByID(u.ID); nu.Name != "Maxi" || !nu.Banned {
		t.Fatalf("Nutzer: %+v", nu)
	}
	if st, _, body := b.get("/users/" + itoa(u.ID)); st != http.StatusOK || !strings.Contains(body, "Maxi") {
		t.Fatal("Nutzerseite")
	}
	b.post("/users/"+itoa(u.ID)+"/delete", url.Values{"csrf": {tok}})
	if _, err := wt.store.UserByID(u.ID); err == nil {
		t.Fatal("Nutzer nicht gelöscht")
	}
	// Deaktiviertes Konto wird sofort abgemeldet
	pb := wt.browser()
	pb.login("papa@example.org")
	b.post("/accounts/"+itoa(papa.ID), url.Values{"csrf": {tok}, "email": {"papa@example.org"}, "name": {"Papa"}, "role": {"user"}, "disabled": {"1"}})
	if st, _, _ := pb.get("/"); st != http.StatusSeeOther {
		t.Fatal("deaktiviertes Konto noch angemeldet")
	}
	// Protokoll enthält die Aktionen
	if _, _, body := b.get("/audit"); !strings.Contains(body, "Raum Fortnite gelöscht") {
		t.Fatal("Protokoll unvollständig")
	}
}

// --- OIDC mit simuliertem Anbieter ---

type fakeIdP struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	mu     sync.Mutex
	nonce  string
	claims map[string]any
}

func newFakeIdP(t *testing.T) *fakeIdP {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	f := &fakeIdP{key: key}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/auth", "token_endpoint": f.srv.URL + "/token",
			"jwks_uri": f.srv.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != "gut" || r.Form.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		f.mu.Lock()
		c := map[string]any{"iss": f.srv.URL, "aud": "talkie", "exp": time.Now().Add(time.Hour).Unix(),
			"iat": time.Now().Unix(), "nonce": f.nonce}
		for k, v := range f.claims {
			c[k] = v
		}
		f.mu.Unlock()
		sig, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
		payload, _ := json.Marshal(c)
		jws, _ := sig.Sign(payload)
		tok, _ := jws.CompactSerialize()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "token_type": "Bearer", "id_token": tok})
	})
	return f
}

// flash liefert die Meldung, die die Login-Seite gerade anzeigt.
func (b *browser) flash() string {
	b.wt.t.Helper()
	_, _, body := b.get("/login")
	m := regexp.MustCompile(`class="flash err">([^<]*)<`).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

// oidcLogin spielt den Ablauf durch: /auth/oidc → Anbieter → Callback.
func (b *browser) oidcLogin(idp *fakeIdP, claims map[string]any) (int, string) {
	b.wt.t.Helper()
	_, loc, _ := b.get("/auth/oidc")
	u, err := url.Parse(loc)
	if err != nil || !strings.HasPrefix(loc, idp.srv.URL+"/auth") {
		b.wt.t.Fatalf("Weiterleitung zum Anbieter: %s", loc)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" {
		b.wt.t.Fatal("kein PKCE")
	}
	idp.mu.Lock()
	idp.nonce, idp.claims = q.Get("nonce"), claims
	idp.mu.Unlock()
	st, loc, _ := b.get("/auth/oidc/callback?code=gut&state=" + url.QueryEscape(q.Get("state")))
	return st, loc
}

// oidcFail meldet sich per OIDC an und liefert die Fehlermeldung der Login-Seite.
func oidcFail(wt *webTest, idp *fakeIdP, claims map[string]any) string {
	b := wt.browser()
	b.oidcLogin(idp, claims)
	return b.flash()
}

func TestWebOIDC(t *testing.T) {
	idp := newFakeIdP(t)
	wt := newWebTest(t, &OIDCConfig{Issuer: idp.srv.URL, ClientID: "talkie", ClientSecret: "geheim", Name: "Pocket ID"})
	wt.account("chef@example.org", RoleAdmin, 0)

	if _, _, body := wt.browser().get("/login"); !strings.Contains(body, "Mit Pocket ID anmelden") {
		t.Fatal("OIDC-Button fehlt")
	}
	// Unbekannte Adresse → kein Zugang (Konten legt nur der Admin an)
	if msg := oidcFail(wt, idp, map[string]any{"sub": "u9", "email": "fremd@example.org", "email_verified": true}); !strings.Contains(msg, "keinen Zugang") {
		t.Fatalf("fremde Adresse: %s", msg)
	}
	// Unbestätigte Adresse → abgelehnt
	if msg := oidcFail(wt, idp, map[string]any{"sub": "u1", "email": "chef@example.org", "email_verified": false}); !strings.Contains(msg, "nicht bestätigt") {
		t.Fatalf("unbestätigte Adresse: %s", msg)
	}
	// Richtige Anmeldung, danach ist die Kennung verknüpft
	b := wt.browser()
	if st, loc := b.oidcLogin(idp, map[string]any{"sub": "u1", "email": "chef@example.org", "email_verified": true}); st != http.StatusSeeOther || loc != "/" {
		t.Fatalf("OIDC-Login: %d %s", st, loc)
	}
	if st, _, _ := b.get("/accounts"); st != http.StatusOK {
		t.Fatal("nach OIDC nicht als Admin angemeldet")
	}
	// Jemand anderes mit derselben Adresse, aber anderer Kennung → abgelehnt
	if msg := oidcFail(wt, idp, map[string]any{"sub": "u2", "email": "chef@example.org", "email_verified": true}); !strings.Contains(msg, "anderen Anmeldung") {
		t.Fatalf("fremde Kennung: %s", msg)
	}
	// Gleiche Kennung, neue Adresse beim Anbieter → klappt weiter
	if _, loc := wt.browser().oidcLogin(idp, map[string]any{"sub": "u1", "email": "neu@example.org", "email_verified": true}); loc != "/" {
		t.Fatalf("bekannte Kennung: %s", loc)
	}
	// Callback ohne passenden State
	if _, loc, _ := wt.browser().get("/auth/oidc/callback?code=gut&state=falsch"); loc != "/login" {
		t.Fatal("Callback ohne State angenommen")
	}
	// Text aus der URL landet nicht auf der Login-Seite
	if _, _, body := wt.browser().get("/login?e=Ruf+0800+an"); strings.Contains(body, "0800") {
		t.Fatal("Text aus der URL wird angezeigt")
	}
}

func TestWebChatClearAndRetention(t *testing.T) {
	wt := newWebTest(t, nil)
	wt.account("chef@x.de", RoleAdmin, 0)
	wt.account("leser@x.de", RoleUser, PermChat)
	rooms, _ := wt.store.Rooms()
	r1, r2 := rooms[0].ID, rooms[1].ID
	now := time.Now().Unix()
	for _, m := range []DBMessage{
		{Room: r1, From: 1, Name: "A", Text: "neu", TS: now},
		{Room: r1, From: 1, Name: "A", Text: "uralt", TS: now - 30*3600},
		{Room: r2, From: 1, Name: "A", Text: "anderer Raum", TS: now},
	} {
		if err := wt.store.AddMessage(m, 100); err != nil {
			t.Fatal(err)
		}
	}
	// 24 h: alte Nachricht wird nicht mehr gezeigt und beim Aufräumen gelöscht
	wt.store.ChatKeep = 24 * time.Hour
	if h, _ := wt.store.History(r1, 50); len(h) != 1 || h[0].Text != "neu" {
		t.Fatalf("Verlauf mit Frist: %+v", h)
	}
	if n, _ := wt.store.PurgeChat(); n != 1 {
		t.Fatalf("aufgeräumt: %d", n)
	}

	// Nur-Lesen-Konto sieht den Chat, aber keinen Leeren-Knopf und darf nicht leeren
	b := wt.browser()
	b.login("leser@x.de")
	if _, _, body := b.get("/chat"); !strings.Contains(body, "neu") || strings.Contains(body, "Alle Räume leeren") {
		t.Fatal("Leser: Chat fehlt oder Leeren-Knopf sichtbar")
	}
	if st, _, _ := b.post("/chat/clear", url.Values{"csrf": {b.csrf()}, "room": {"0"}}); st != http.StatusForbidden {
		t.Fatalf("Leser durfte leeren: %d", st)
	}

	a := wt.browser()
	a.login("chef@x.de")
	if _, _, body := a.get("/chat"); !strings.Contains(body, "nach 24 Stunden") || !strings.Contains(body, "Alle Räume leeren") {
		t.Fatal("Admin: Hinweis oder Knopf fehlt")
	}
	a.post("/chat/clear", url.Values{"csrf": {a.csrf()}, "room": {itoa(r1)}})
	if h, _ := wt.store.History(r1, 50); len(h) != 0 {
		t.Fatalf("Raum nicht geleert: %+v", h)
	}
	if h, _ := wt.store.History(r2, 50); len(h) != 1 {
		t.Fatal("anderer Raum wurde mitgelöscht")
	}
	a.post("/chat/clear", url.Values{"csrf": {a.csrf()}, "room": {"0"}})
	if h, _ := wt.store.History(r2, 50); len(h) != 0 {
		t.Fatal("alle Räume nicht geleert")
	}
	if log, _ := wt.store.AuditLog(5); len(log) < 2 || !strings.Contains(log[0].Action, "allen Räumen") {
		t.Fatalf("Protokoll: %+v", log)
	}
}

func TestWebDevices(t *testing.T) {
	wt := newWebTest(t, nil)
	wt.account("chef@x.de", RoleAdmin, 0)
	wt.account("helfer@x.de", RoleUser, PermUsersEdit)
	inv, _ := wt.store.CreateInvite(1, time.Hour)
	pk1 := make([]byte, 32)
	pk1[0] = 1
	u, err := wt.store.Register(inv.Code, "Mia", pk1)
	if err != nil {
		t.Fatal(err)
	}
	b := wt.browser()
	b.login("chef@x.de")
	if _, _, body := b.get("/users/" + itoa(u.ID)); !strings.Contains(body, "Geräte-Code erstellen") {
		t.Fatal("Geräte-Abschnitt fehlt")
	}
	// Nicht-Admin mit „Nutzer bearbeiten“ darf keine Geräte-Codes erstellen oder sehen
	h := wt.browser()
	h.login("helfer@x.de")
	if st, _, _ := h.post("/users/"+itoa(u.ID)+"/device_code", url.Values{"csrf": {h.csrf()}}); st != http.StatusForbidden {
		t.Fatalf("Helfer durfte Code erstellen: %d", st)
	}
	if wt.store.DeviceCode(u.ID) != nil {
		t.Fatal("Code trotz Verbot erstellt")
	}
	b.post("/users/"+itoa(u.ID)+"/device_code", url.Values{"csrf": {b.csrf()}})
	code := wt.store.DeviceCode(u.ID)
	if _, _, body := h.get("/users/" + itoa(u.ID)); strings.Contains(body, code.Code) || strings.Contains(body, "Geräte-Code erstellen") {
		t.Fatal("Helfer sieht den Code oder den Knopf")
	}
	if code == nil {
		t.Fatal("kein Code")
	}
	if _, _, body := b.get("/users/" + itoa(u.ID)); !strings.Contains(body, code.Code) {
		t.Fatal("Code wird nicht angezeigt")
	}
	pk2 := make([]byte, 32)
	pk2[0] = 2
	if _, err := wt.store.RegisterDevice(code.Code, "", pk2, "iOS"); err != nil {
		t.Fatal(err)
	}
	devs, _ := wt.store.Devices(u.ID)
	if len(devs) != 2 {
		t.Fatalf("geräte: %d", len(devs))
	}
	if _, _, body := b.get("/users/" + itoa(u.ID)); !strings.Contains(body, "iOS") {
		t.Fatal("Gerätename fehlt")
	}
	var ios int64
	for _, d := range devs {
		if d.Label == "iOS" {
			ios = d.ID
		}
	}
	b.post("/users/"+itoa(u.ID)+"/device_delete", url.Values{"csrf": {b.csrf()}, "device": {itoa(ios)}})
	if devs, _ := wt.store.Devices(u.ID); len(devs) != 1 {
		t.Fatalf("nicht entfernt: %d", len(devs))
	}
}

func TestInviteMailInstallHints(t *testing.T) {
	wt := newWebTest(t, nil)
	inv := &DBInvite{Code: "ABCD2345", Expires: time.Now().Add(24 * time.Hour)}

	body := wt.app.inviteMail(inv, "Manuel")
	if !strings.Contains(body, appStoreURL) || strings.Contains(body, "Talkie-Windows.zip") {
		t.Fatalf("ohne Download-Link nur App Store erwartet:\n%s", body)
	}

	wt.app.cfg.DownloadURL = "https://example.test/releases/latest"
	body = wt.app.inviteMail(inv, "Manuel")
	for _, want := range []string{appStoreURL, "https://example.test/releases/latest", "Talkie-macOS.dmg",
		"Talkie-Windows.zip", "Talkie-Android.apk", "ABCD2345", "talkie.test:7777", "4. Einladungscode", "Auf weiterem Gerät nutzen"} {
		if !strings.Contains(body, want) {
			t.Errorf("%q fehlt in der Mail:\n%s", want, body)
		}
	}
}

func TestInviteMailHTML(t *testing.T) {
	wt := newWebTest(t, nil)
	inv := &DBInvite{Code: "ABCD2345", Expires: time.Now().Add(24 * time.Hour)}
	wt.app.cfg.ServerAddr = ""
	h := wt.app.inviteMailHTML(inv, "<script>alert(1)</script>")
	if !strings.Contains(h, "ABCD2345") || !strings.Contains(h, "cid:talkie-icon") || !strings.Contains(h, appStoreURL) {
		t.Fatalf("Code, Icon oder App Store fehlt:\n%s", h)
	}
	if strings.Contains(h, "<script>") || !strings.Contains(h, "&lt;script&gt;") {
		t.Fatal("Absendername nicht escaped")
	}
	if strings.Contains(h, "data:image") || strings.Contains(h, "Talkie-Windows.zip") || strings.Contains(h, "Server</b>") {
		t.Fatal("Download- oder Server-Schritt ohne Konfiguration sichtbar")
	}
	if !strings.Contains(h, ">3</div>") || strings.Contains(h, ">4</div>") {
		t.Fatal("Schrittnummern ohne Server falsch")
	}
	wt.app.cfg.DownloadURL = "https://example.test/dl"
	wt.app.cfg.ServerAddr = "talkie.test:7777"
	h = wt.app.inviteMailHTML(inv, "Manuel")
	for _, want := range []string{"https://example.test/dl", "Talkie-Windows.zip", "talkie.test:7777", ">4</div>"} {
		if !strings.Contains(h, want) {
			t.Errorf("%q fehlt", want)
		}
	}
}

func TestLoginMailHTML(t *testing.T) {
	h := renderMail("login.html", map[string]any{"Name": "<b>x</b>", "Code": "1234 5678", "Link": "https://t.test/login/link?t=abc&x=1"})
	for _, want := range []string{"1234 5678", "cid:talkie-icon", "https://t.test/login/link?t=abc&amp;x=1", "&lt;b&gt;x"} {
		if !strings.Contains(h, want) {
			t.Errorf("%q fehlt:\n%s", want, h)
		}
	}
}

func TestBuildMailMIME(t *testing.T) {
	from := &mail.Address{Name: "Talkie", Address: "talkie@example.test"}
	raw := buildMail(from, "a@example.test", "Betreff ä", "Text ä\nzwei", "<p>"+strings.Repeat("lang ä ", 500)+"</p>")
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("Content-Type %q %v", mt, err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if len(line) > 998 {
			t.Fatalf("Zeile zu lang: %d", len(line))
		}
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var kinds []string
	var cid string
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		pt, pp, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		kinds = append(kinds, pt)
		if pt == "multipart/related" {
			rr := multipart.NewReader(p, pp["boundary"])
			for {
				q, err := rr.NextPart()
				if err != nil {
					break
				}
				kinds = append(kinds, q.Header.Get("Content-Type"))
				if c := q.Header.Get("Content-ID"); c != "" {
					cid = c
				}
			}
		}
	}
	if len(kinds) != 4 || kinds[0] != "text/plain" || kinds[1] != "multipart/related" || cid != "<talkie-icon>" {
		t.Fatalf("Aufbau falsch: %v cid=%q", kinds, cid)
	}
	plain := buildMail(from, "a@example.test", "x", "nur Text", "")
	if !strings.Contains(string(plain), "text/plain") || strings.Contains(string(plain), "multipart") {
		t.Fatal("ohne HTML nur Text erwartet")
	}
}

func TestTempRoomNamesInWeb(t *testing.T) {
	wt := newWebTest(t, nil)
	wt.account("chef@example.org", "admin", 0)
	b := wt.browser()
	b.login("chef@example.org")
	tok := b.csrf()
	wt.hub.mu.Lock()
	wt.hub.temp = append(wt.hub.temp, &tempRoom{id: tempRoomFirstID, name: "Geheim"})
	wt.hub.mu.Unlock()
	if got := wt.app.roomNames()[tempRoomFirstID]; got != "Geheim (App-Raum)" {
		t.Fatalf("Raumname: %q", got)
	}
	dbCount := func() int { r, _ := wt.store.Rooms(); return len(r) }
	before := dbCount()
	b.post("/rooms", url.Values{"csrf": {tok}, "name": {"GEHEIM"}})
	if dbCount() != before {
		t.Fatal("Raum mit Namen eines App-Raums wurde angelegt")
	}
	if f := b.flash(); !strings.Contains(f, "aus der App") {
		t.Fatalf("Meldung: %q", f)
	}
	b.post("/rooms", url.Values{"csrf": {tok}, "name": {"Anders"}})
	if dbCount() != before+1 {
		t.Fatal("Raum ohne Namenskollision nicht angelegt")
	}
}

func TestRoomsPageShowsTempRooms(t *testing.T) {
	wt := newWebTest(t, nil)
	wt.account("chef@example.org", "admin", 0)
	b := wt.browser()
	b.login("chef@example.org")
	_, _, body := b.get("/rooms")
	if !strings.Contains(body, "Gerade gibt es keine Räume aus der App.") {
		t.Fatal("Leer-Text fehlt")
	}
	salt := []byte("salz")
	wt.hub.mu.Lock()
	wt.hub.sessions[77] = &Session{hub: wt.hub, userID: 77, name: "Emil", room: tempRoomFirstID,
		out: make(chan []byte, 4), done: make(chan struct{})}
	wt.hub.temp = append(wt.hub.temp,
		&tempRoom{id: tempRoomFirstID, name: "Zockerhöhle", creator: 77, salt: salt, hash: hashPassword(salt, "streng-geheim")},
		&tempRoom{id: tempRoomFirstID + 1, name: "Offenes Zimmer", creator: 77})
	wt.hub.mu.Unlock()
	_, _, body = b.get("/rooms")
	for _, want := range []string{"Räume aus der App", "Zockerhöhle", "Emil", "mit Passwort", "Offenes Zimmer", "offen"} {
		if !strings.Contains(body, want) {
			t.Fatalf("%q fehlt", want)
		}
	}
	if strings.Contains(body, "streng-geheim") || strings.Contains(body, "salz") {
		t.Fatal("Passwortdaten im HTML")
	}
	if strings.Contains(body, "Gerade gibt es keine Räume aus der App.") {
		t.Fatal("Leer-Text trotz Räumen")
	}
}
