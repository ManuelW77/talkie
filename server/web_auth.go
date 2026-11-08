package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const pendCookie = "talkie_login"

func (w *webApp) loginPage(rw http.ResponseWriter, r *http.Request) {
	w.render(rw, r, "login", "Anmelden", map[string]any{"Error": r.URL.Query().Get("e")})
}

func eightDigits() string {
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%08d", n.Int64())
}

// loginMail verschickt Link + Code. Die Antwort ist immer gleich, egal ob es
// das Konto gibt (sonst ließe sich herausfinden, welche Adressen existieren).
func (w *webApp) loginMail(rw http.ResponseWriter, r *http.Request) {
	if w.cfg.Mailer == nil {
		http.Redirect(rw, r, "/login", http.StatusSeeOther)
		return
	}
	if !w.mailLimit.Reserve(w.clientIP(r)) {
		w.render(rw, r, "login", "Anmelden", map[string]any{"Error": "Zu viele Versuche. Bitte warte ein paar Minuten."})
		return
	}
	email := normEmail(r.PostFormValue("email"))
	pend := randToken(32)
	if acc, err := w.store.AccountByEmail(email); err == nil && !acc.Disabled {
		if n, err := w.store.RecentLogins(acc.ID, 15*time.Minute); err == nil && n < 3 {
			link, code := randToken(32), eightDigits()
			if err := w.store.CreateLogin(acc.ID, link, pend, code); err != nil {
				log.Printf("web: login anlegen: %v", err)
			} else {
				go w.sendLoginMail(acc, link, code)
			}
		}
	}
	http.SetCookie(rw, &http.Cookie{Name: pendCookie, Value: pend, Path: "/login", MaxAge: int(loginTTL.Seconds()),
		HttpOnly: true, Secure: w.secure, SameSite: http.SameSiteLaxMode})
	http.Redirect(rw, r, "/login/code", http.StatusSeeOther)
}

func (w *webApp) sendLoginMail(acc *Account, link, code string) {
	body := fmt.Sprintf(`Hallo %s,

dein Anmeldecode für die Talkie-Verwaltung:

    %s %s

Oder melde dich mit diesem Link an:
%s/login/link?t=%s

Code und Link gelten 15 Minuten und nur einmal.
Wenn du das nicht angefordert hast, ignoriere diese Mail einfach.
`, acc.Name, code[:4], code[4:], w.cfg.PublicURL, link)
	if err := w.cfg.Mailer.Send(acc.Email, "Dein Talkie-Anmeldecode: "+code[:4]+" "+code[4:], body); err != nil {
		log.Printf("web: login-mail an %s: %v", acc.Email, err)
	}
}

func (w *webApp) codePage(rw http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(pendCookie); err != nil {
		http.Redirect(rw, r, "/login", http.StatusSeeOther)
		return
	}
	w.render(rw, r, "code", "Code eingeben", map[string]any{})
}

func (w *webApp) loginCode(rw http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(pendCookie)
	if err != nil {
		http.Redirect(rw, r, "/login", http.StatusSeeOther)
		return
	}
	if !w.codeLimit.Reserve(w.clientIP(r)) {
		w.render(rw, r, "code", "Code eingeben", map[string]any{"Error": "Zu viele Versuche. Bitte warte ein paar Minuten."})
		return
	}
	code := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, r.PostFormValue("code"))
	id, err := w.store.LoginByCode(c.Value, code)
	if err != nil {
		w.render(rw, r, "code", "Code eingeben", map[string]any{"Error": "Der Code stimmt nicht oder ist abgelaufen."})
		return
	}
	w.finishLogin(rw, r, id, "Mail-Code")
}

func (w *webApp) linkPage(rw http.ResponseWriter, r *http.Request) {
	// Nur anzeigen, erst der Klick (POST) meldet an: Virenscanner, die Links in
	// Mails vorab öffnen, verbrauchen ihn so nicht.
	w.render(rw, r, "link", "Anmelden", map[string]any{"T": r.URL.Query().Get("t")})
}

func (w *webApp) loginLink(rw http.ResponseWriter, r *http.Request) {
	if !w.codeLimit.Reserve(w.clientIP(r)) {
		http.Redirect(rw, r, "/login?e="+urlq("Zu viele Versuche. Bitte warte ein paar Minuten."), http.StatusSeeOther)
		return
	}
	id, err := w.store.LoginByLink(r.PostFormValue("t"))
	if err != nil {
		http.Redirect(rw, r, "/login?e="+urlq("Der Link ist abgelaufen oder wurde schon benutzt."), http.StatusSeeOther)
		return
	}
	w.finishLogin(rw, r, id, "Mail-Link")
}

func (w *webApp) finishLogin(rw http.ResponseWriter, r *http.Request, id int64, how string) {
	acc, err := w.store.AccountByID(id)
	if err != nil || acc.Disabled {
		http.Redirect(rw, r, "/login?e="+urlq("Dieses Konto ist deaktiviert."), http.StatusSeeOther)
		return
	}
	http.SetCookie(rw, &http.Cookie{Name: pendCookie, Path: "/login", MaxAge: -1})
	w.startSession(rw, r, acc, how)
}

func (w *webApp) logout(rw http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(w.cookie); err == nil {
		_ = w.store.DeleteSession(c.Value)
	}
	http.SetCookie(rw, w.sessionCookie("", -1))
	http.Redirect(rw, r, "/login", http.StatusSeeOther)
}

// --- OIDC (Pocket ID, Authentik, Keycloak, Google, …) ---

func (w *webApp) oidcClient(ctx context.Context) (*oauth2.Config, *oidc.IDTokenVerifier, error) {
	w.oidcMu.Lock()
	defer w.oidcMu.Unlock()
	if w.oauth != nil {
		return w.oauth, w.verifier, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	prov, err := oidc.NewProvider(ctx, w.cfg.OIDC.Issuer)
	if err != nil {
		return nil, nil, err
	}
	w.oauth = &oauth2.Config{
		ClientID:     w.cfg.OIDC.ClientID,
		ClientSecret: w.cfg.OIDC.ClientSecret,
		Endpoint:     prov.Endpoint(),
		RedirectURL:  w.cfg.PublicURL + "/auth/oidc/callback",
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
	w.verifier = prov.Verifier(&oidc.Config{ClientID: w.cfg.OIDC.ClientID})
	return w.oauth, w.verifier, nil
}

func (w *webApp) oidcStart(rw http.ResponseWriter, r *http.Request) {
	if w.cfg.OIDC == nil {
		http.NotFound(rw, r)
		return
	}
	oc, _, err := w.oidcClient(r.Context())
	if err != nil {
		log.Printf("web: oidc: %v", err)
		http.Redirect(rw, r, "/login?e="+urlq(w.cfg.OIDC.Name+" ist gerade nicht erreichbar."), http.StatusSeeOther)
		return
	}
	state, nonce, ver := randToken(24), randToken(24), oauth2.GenerateVerifier()
	now := time.Now()
	w.oidcMu.Lock()
	for k, f := range w.flows {
		if now.After(f.expires) {
			delete(w.flows, k)
		}
	}
	if len(w.flows) > 1000 {
		w.oidcMu.Unlock()
		http.Error(rw, "Zu viele Anmeldungen gleichzeitig", http.StatusTooManyRequests)
		return
	}
	w.flows[state] = oidcFlow{nonce: nonce, verifier: ver, expires: now.Add(10 * time.Minute)}
	w.oidcMu.Unlock()
	http.SetCookie(rw, &http.Cookie{Name: "talkie_oidc", Value: state, Path: "/auth/oidc", MaxAge: 600,
		HttpOnly: true, Secure: w.secure, SameSite: http.SameSiteLaxMode})
	http.Redirect(rw, r, oc.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(ver)), http.StatusFound)
}

func (w *webApp) oidcCallback(rw http.ResponseWriter, r *http.Request) {
	bad := func(msg string) {
		http.Redirect(rw, r, "/login?e="+urlq(msg), http.StatusSeeOther)
	}
	if w.cfg.OIDC == nil {
		http.NotFound(rw, r)
		return
	}
	http.SetCookie(rw, &http.Cookie{Name: "talkie_oidc", Path: "/auth/oidc", MaxAge: -1})
	c, err := r.Cookie("talkie_oidc")
	state := r.URL.Query().Get("state")
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1 {
		bad("Anmeldung abgelaufen, bitte noch einmal.")
		return
	}
	w.oidcMu.Lock()
	flow, ok := w.flows[state]
	delete(w.flows, state)
	w.oidcMu.Unlock()
	if !ok || time.Now().After(flow.expires) {
		bad("Anmeldung abgelaufen, bitte noch einmal.")
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		bad("Anmeldung abgebrochen.")
		return
	}
	oc, verifier, err := w.oidcClient(r.Context())
	if err != nil {
		bad(w.cfg.OIDC.Name + " ist gerade nicht erreichbar.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tok, err := oc.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(flow.verifier))
	if err != nil {
		log.Printf("web: oidc exchange: %v", err)
		bad("Anmeldung fehlgeschlagen.")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := verifier.Verify(ctx, raw)
	if err != nil || subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(flow.nonce)) != 1 {
		log.Printf("web: oidc token: %v", err)
		bad("Anmeldung fehlgeschlagen.")
		return
	}
	var claims struct {
		Email    string `json:"email"`
		Verified *bool  `json:"email_verified"`
	}
	if err := idt.Claims(&claims); err != nil {
		bad("Anmeldung fehlgeschlagen.")
		return
	}
	acc, err := w.oidcAccount(idt.Issuer+"|"+idt.Subject, claims.Email, claims.Verified)
	if err != nil {
		var ue userError
		if errors.As(err, &ue) {
			bad(ue.Error())
		} else {
			log.Printf("web: oidc konto: %v", err)
			bad("Anmeldung fehlgeschlagen.")
		}
		return
	}
	w.startSession(rw, r, acc, w.cfg.OIDC.Name)
}

// oidcAccount ordnet eine OIDC-Identität einem Web-Konto zu: zuerst über die
// gespeicherte Kennung (sub), beim ersten Mal über die E-Mail-Adresse.
// Konten werden nie automatisch angelegt – das macht der Admin.
func (w *webApp) oidcAccount(sub, email string, verified *bool) (*Account, error) {
	notAllowed := userError("Für dieses Konto gibt es keinen Zugang. Bitte den Admin, dich einzutragen.")
	acc, err := w.store.AccountBySub(sub)
	if errors.Is(err, ErrNotFound) {
		if _, perr := mail.ParseAddress(email); email == "" || perr != nil {
			return nil, notAllowed
		}
		if (verified == nil || !*verified) && !w.cfg.OIDC.AllowUnverifiedEmail {
			return nil, userError("Deine E-Mail-Adresse ist beim Anmeldedienst nicht bestätigt.")
		}
		acc, err = w.store.AccountByEmail(email)
		if errors.Is(err, ErrNotFound) {
			return nil, notAllowed
		}
		if err != nil {
			return nil, err
		}
		if acc.OIDCSub != "" && acc.OIDCSub != sub {
			return nil, userError("Dieses Konto ist schon mit einer anderen Anmeldung verknüpft. Der Admin kann das zurücksetzen.")
		}
		if err := w.store.SetOIDCSub(acc.ID, sub); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if acc.Disabled {
		return nil, userError("Dieses Konto ist deaktiviert.")
	}
	return acc, nil
}
