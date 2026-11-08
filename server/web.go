package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Admin-Webseite: Web-Konten (E-Mail) melden sich per Mail-Link/-Code oder
// OIDC an und verwalten Talkie-Nutzer, Einladungen und Räume – je nach Rechten.

//go:embed web
var webFS embed.FS

type OIDCConfig struct {
	Issuer, ClientID, ClientSecret string
	Name                           string // Beschriftung des Buttons, z. B. „Pocket ID“
	AllowUnverifiedEmail           bool
}

type WebConfig struct {
	PublicURL   string // z. B. https://talkie.example.org (ohne / am Ende)
	TrustProxy  bool   // X-Forwarded-For auswerten (nur hinter eigenem Reverse-Proxy)
	OIDC        *OIDCConfig
	Mailer      Mailer // nil = Login per Mail aus
	ServerAddr  string // Adresse für die App, steht in Einladungsmails
	DownloadURL string // wo es die App gibt, steht in Einladungsmails
}

const sessionTTL = 30 * 24 * time.Hour

type webApp struct {
	hub   *Hub
	store *Store
	cfg   WebConfig

	pages     map[string]*template.Template
	secure    bool
	cookie    string
	origin    string
	mailLimit *IPLimiter // Login-Mails pro IP
	codeLimit *IPLimiter // Code-Eingaben pro IP

	oidcMu   sync.Mutex
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
	flows    map[string]oidcFlow
}

type oidcFlow struct {
	nonce, verifier string
	expires         time.Time
}

func newWebApp(hub *Hub, store *Store, cfg WebConfig) (*webApp, error) {
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	u, err := url.Parse(cfg.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("TALKIE_PUBLIC_URL muss eine vollständige Adresse sein, z. B. https://talkie.example.org")
	}
	w := &webApp{
		hub: hub, store: store, cfg: cfg,
		secure:    u.Scheme == "https",
		origin:    u.Scheme + "://" + u.Host,
		mailLimit: NewIPLimiter(10, 10*time.Minute),
		codeLimit: NewIPLimiter(30, 10*time.Minute),
		flows:     map[string]oidcFlow{},
	}
	w.cookie = "talkie_session"
	if w.secure {
		w.cookie = "__Host-talkie_session"
	}
	if err := w.loadTemplates(); err != nil {
		return nil, err
	}
	return w, nil
}

var tplFuncs = template.FuncMap{
	"dt": func(t time.Time) string {
		if t.IsZero() {
			return "–"
		}
		return t.Local().Format("02.01.2006 15:04")
	},
	"date": func(t time.Time) string { return t.Local().Format("02.01.2006") },
	"unix": func(ts int64) string { return time.Unix(ts, 0).Local().Format("02.01. 15:04") },
}

func (w *webApp) loadTemplates() error {
	w.pages = map[string]*template.Template{}
	files, err := fs.Glob(webFS, "web/pages/*.html")
	if err != nil {
		return err
	}
	for _, f := range files {
		t, err := template.New("").Funcs(tplFuncs).ParseFS(webFS, "web/layout.html", f)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(f[strings.LastIndex(f, "/")+1:], ".html")
		w.pages[name] = t
	}
	return nil
}

// --- Seitenaufbau ---

type page struct {
	Title    string
	Me       *Account
	CSRF     string
	Can      map[string]bool
	Flash    string
	FlashErr bool
	Nav      string
	OIDCName string
	MailOn   bool
	D        map[string]any
}

type ctxKey int

const (
	ctxAccount ctxKey = iota
	ctxCSRF
)

func me(r *http.Request) *Account {
	a, _ := r.Context().Value(ctxAccount).(*Account)
	return a
}

func (w *webApp) render(rw http.ResponseWriter, r *http.Request, name, title string, d map[string]any) {
	p := page{Title: title, Me: me(r), Nav: name, MailOn: w.cfg.Mailer != nil, D: d, Can: map[string]bool{}}
	if w.cfg.OIDC != nil {
		p.OIDCName = w.cfg.OIDC.Name
	}
	if p.Me != nil {
		p.CSRF, _ = r.Context().Value(ctxCSRF).(string)
		for _, pl := range permList {
			p.Can[pl.Key] = p.Me.Can(pl.P)
		}
		p.Can["admin"] = p.Me.IsAdmin()
	}
	if c, err := r.Cookie("talkie_flash"); err == nil {
		if v, err := url.QueryUnescape(c.Value); err == nil {
			p.FlashErr = strings.HasPrefix(v, "!")
			p.Flash = strings.TrimPrefix(v, "!")
		}
		http.SetCookie(rw, &http.Cookie{Name: "talkie_flash", Path: "/", MaxAge: -1})
	}
	t := w.pages[name]
	if t == nil {
		http.Error(rw, "Seite fehlt", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(rw, "layout", p); err != nil {
		log.Printf("web: template %s: %v", name, err)
	}
}

// done leitet nach einer Aktion weiter und zeigt eine Meldung an.
func (w *webApp) done(rw http.ResponseWriter, r *http.Request, to, msg string, isErr bool) {
	if isErr {
		msg = "!" + msg
	}
	http.SetCookie(rw, &http.Cookie{Name: "talkie_flash", Value: url.QueryEscape(msg), Path: "/", MaxAge: 60,
		HttpOnly: true, Secure: w.secure, SameSite: http.SameSiteLaxMode})
	http.Redirect(rw, r, to, http.StatusSeeOther)
}

func (w *webApp) fail(rw http.ResponseWriter, r *http.Request, to string, err error) {
	msg := err.Error()
	switch {
	case errors.Is(err, ErrNotFound):
		msg = "Nicht gefunden."
	case errors.Is(err, ErrNameTaken):
		msg = "Der Name ist schon vergeben."
	case errors.Is(err, ErrLastAdmin):
	default:
		var ue userError
		if !errors.As(err, &ue) {
			log.Printf("web: %v", err)
			msg = "Da ist etwas schiefgegangen."
		}
	}
	w.done(rw, r, to, msg, true)
}

// userError ist eine Meldung, die so dem Nutzer angezeigt werden darf.
type userError string

func (e userError) Error() string { return string(e) }

func (w *webApp) audit(r *http.Request, action string) {
	who := "?"
	if a := me(r); a != nil {
		who = a.Email
	}
	w.store.Audit(who, action)
}

// --- Middleware ---

func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (w *webApp) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if w.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			host = strings.TrimSpace(parts[len(parts)-1])
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		return ipKey(ip)
	}
	return host
}

func (w *webApp) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		h := rw.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// same-origin statt no-referrer: Bei no-referrer schicken Browser bei Formularen
		// „Origin: null“, und die Origin-Prüfung unten würde jedes Formular ablehnen.
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		if w.secure {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		// Formulare nur von der eigenen Seite (Schutz gegen fremde Seiten, die hierher posten)
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			o := r.Header.Get("Origin")
			// Mail-Programme öffnen Links teils in abgeschotteten Fenstern, die „Origin: null“
			// schicken. Beim Link-Login ist das geheime Token selbst der Nachweis.
			linkLogin := r.URL.Path == "/login/link" && o == "null"
			if !linkLogin && ((o != "" && o != w.origin) || r.Header.Get("Sec-Fetch-Site") == "cross-site") {
				log.Printf("web: %s %s abgelehnt: Origin %q, erwartet %q", r.Method, r.URL.Path, o, w.origin)
				http.Error(rw, "Anfrage von fremder Seite abgelehnt. Bitte die Seite über "+w.cfg.PublicURL+" öffnen.", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(rw, r)
	})
}

// auth verlangt eine gültige Sitzung (und bei POST das CSRF-Token) sowie das Recht perm.
// perm 0 = jedes angemeldete Konto, permAdmin = nur Admins.
const permAdmin Perm = -1

func (w *webApp) auth(perm Perm, h http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(w.cookie)
		if err != nil {
			http.Redirect(rw, r, "/login", http.StatusSeeOther)
			return
		}
		acc, csrf, err := w.store.SessionAccount(c.Value)
		if err != nil {
			http.SetCookie(rw, w.sessionCookie("", -1))
			http.Redirect(rw, r, "/login", http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost {
			if subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf")), []byte(csrf)) != 1 {
				http.Error(rw, "Sitzung abgelaufen – bitte Seite neu laden.", http.StatusForbidden)
				return
			}
		}
		allowed := perm == 0 || (perm == permAdmin && acc.IsAdmin()) || (perm > 0 && acc.Can(perm))
		ctx := context.WithValue(r.Context(), ctxAccount, acc)
		ctx = context.WithValue(ctx, ctxCSRF, csrf)
		r = r.WithContext(ctx)
		if !allowed {
			rw.WriteHeader(http.StatusForbidden)
			w.render(rw, r, "forbidden", "Kein Zugriff", nil)
			return
		}
		h(rw, r)
	}
}

func (w *webApp) sessionCookie(val string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: w.cookie, Value: val, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: w.secure, SameSite: http.SameSiteLaxMode}
}

func (w *webApp) startSession(rw http.ResponseWriter, r *http.Request, acc *Account, how string) {
	tok := randToken(32)
	if err := w.store.CreateSession(acc.ID, tok, randToken(24), sessionTTL); err != nil {
		log.Printf("web: sitzung: %v", err)
		http.Error(rw, "Anmeldung fehlgeschlagen", http.StatusInternalServerError)
		return
	}
	w.store.Audit(acc.Email, "angemeldet ("+how+")")
	http.SetCookie(rw, w.sessionCookie(tok, int(sessionTTL.Seconds())))
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}

func (w *webApp) handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web/static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))

	// Anmeldung
	mux.HandleFunc("GET /login", w.loginPage)
	mux.HandleFunc("POST /login", w.loginMail)
	mux.HandleFunc("GET /login/code", w.codePage)
	mux.HandleFunc("POST /login/code", w.loginCode)
	mux.HandleFunc("GET /login/link", w.linkPage)
	mux.HandleFunc("POST /login/link", w.loginLink)
	mux.HandleFunc("GET /auth/oidc", w.oidcStart)
	mux.HandleFunc("GET /auth/oidc/callback", w.oidcCallback)
	mux.HandleFunc("POST /logout", w.auth(0, w.logout))

	// Verwaltung
	mux.HandleFunc("GET /{$}", w.auth(0, w.dashboard))
	mux.HandleFunc("GET /users", w.auth(0, w.usersPage))
	mux.HandleFunc("GET /users/{id}", w.auth(0, w.userPage))
	mux.HandleFunc("POST /users/{id}/{action}", w.auth(0, w.userAction))
	mux.HandleFunc("GET /invites", w.auth(PermInvite, w.invitesPage))
	mux.HandleFunc("POST /invites", w.auth(PermInvite, w.createInvite))
	mux.HandleFunc("POST /invites/{code}/delete", w.auth(PermInvite, w.deleteInvite))
	mux.HandleFunc("GET /rooms", w.auth(0, w.roomsPage))
	mux.HandleFunc("POST /rooms", w.auth(PermRooms, w.addRoom))
	mux.HandleFunc("POST /rooms/{id}/{action}", w.auth(PermRooms, w.roomAction))
	mux.HandleFunc("GET /chat", w.auth(PermChat, w.chatPage))
	mux.HandleFunc("GET /accounts", w.auth(permAdmin, w.accountsPage))
	mux.HandleFunc("GET /accounts/new", w.auth(permAdmin, w.accountForm))
	mux.HandleFunc("POST /accounts/new", w.auth(permAdmin, w.accountSave))
	mux.HandleFunc("GET /accounts/{id}", w.auth(permAdmin, w.accountForm))
	mux.HandleFunc("POST /accounts/{id}", w.auth(permAdmin, w.accountSave))
	mux.HandleFunc("POST /accounts/{id}/delete", w.auth(permAdmin, w.accountDelete))
	mux.HandleFunc("GET /audit", w.auth(permAdmin, w.auditPage))

	return w.securityHeaders(http.MaxBytesHandler(mux, 64<<10))
}
