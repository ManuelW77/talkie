package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	dataDir := env("TALKIE_DATA", "./data")
	addr := env("TALKIE_ADDR", ":7777")
	adminAddr := env("TALKIE_ADMIN_ADDR", "127.0.0.1:7778")
	maxUsers, _ := strconv.Atoi(env("TALKIE_MAX_USERS", "50"))

	if len(os.Args) > 1 && os.Args[1] != "serve" {
		if err := runCLI(os.Args[1:], dataDir, adminAddr); err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			os.Exit(1)
		}
		return
	}
	if err := serve(dataDir, addr, adminAddr, maxUsers); err != nil {
		log.Fatal(err)
	}
}

func serve(dataDir, addr, adminAddr string, maxUsers int) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	cert, err := LoadOrCreateCert(dataDir)
	if err != nil {
		return fmt.Errorf("zertifikat: %w", err)
	}
	store, err := OpenStore(filepath.Join(dataDir, "talkie.db"))
	if err != nil {
		return fmt.Errorf("datenbank: %w", err)
	}
	defer store.Close()
	hash := CertHash(cert)
	hub, err := NewHub(store, hash, maxUsers)
	if err != nil {
		return err
	}
	token, err := loadOrCreateToken(dataDir)
	if err != nil {
		return fmt.Errorf("admin-token: %w", err)
	}

	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{ALPN},
		MinVersion:   tls.VersionTLS13,
	}
	qconf := &quic.Config{
		EnableDatagrams:       true,
		KeepAlivePeriod:       10 * time.Second,
		MaxIdleTimeout:        30 * time.Second,
		MaxIncomingStreams:    4,
		MaxIncomingUniStreams: -1,
	}
	ln, err := quic.ListenAddr(addr, tlsConf, qconf)
	if err != nil {
		return fmt.Errorf("lauschen auf %s: %w", addr, err)
	}
	defer ln.Close()

	var webSrv *http.Server
	if web, err := webFromEnv(hub, store); err != nil {
		return fmt.Errorf("webseite: %w", err)
	} else if web != nil {
		webAddr := env("TALKIE_WEB_ADDR", ":8080")
		webSrv = &http.Server{Addr: webAddr, Handler: web.handler(),
			ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second}
		go func() {
			if err := webSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("webseite: %v", err)
			}
		}()
		log.Printf("Verwaltungsseite auf %s (%s)", webAddr, web.cfg.PublicURL)
	} else {
		log.Printf("Verwaltungsseite aus (TALKIE_PUBLIC_URL nicht gesetzt)")
	}

	adminSrv := &http.Server{Addr: adminAddr, Handler: (&adminAPI{hub: hub, store: store, token: token}).handler(),
		ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := adminSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("admin-api: %v", err)
		}
	}()

	log.Printf("Talkie-Server läuft auf UDP %s", addr)
	log.Printf("Fingerabdruck: %s", FormatFingerprint(hash))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		log.Printf("beende …")
		_ = adminSrv.Close()
		if webSrv != nil {
			_ = webSrv.Close()
		}
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go hub.HandleConn(conn)
	}
}

// webFromEnv baut die Verwaltungsseite aus Umgebungsvariablen (nil = aus).
func webFromEnv(hub *Hub, store *Store) (*webApp, error) {
	pub := env("TALKIE_PUBLIC_URL", "")
	if pub == "" {
		return nil, nil
	}
	cfg := WebConfig{
		PublicURL:   pub,
		TrustProxy:  env("TALKIE_TRUST_PROXY", "") == "1",
		ServerAddr:  env("TALKIE_SERVER_ADDR", ""),
		DownloadURL: env("TALKIE_DOWNLOAD_URL", ""),
	}
	if sc := smtpFromEnv(); sc != nil {
		cfg.Mailer = &smtpMailer{c: *sc}
	}
	if iss := env("TALKIE_OIDC_ISSUER", ""); iss != "" {
		cfg.OIDC = &OIDCConfig{
			Issuer:               iss,
			ClientID:             env("TALKIE_OIDC_CLIENT_ID", ""),
			ClientSecret:         env("TALKIE_OIDC_CLIENT_SECRET", ""),
			Name:                 env("TALKIE_OIDC_NAME", "OIDC"),
			AllowUnverifiedEmail: env("TALKIE_OIDC_ALLOW_UNVERIFIED", "") == "1",
		}
		if cfg.OIDC.ClientID == "" {
			return nil, errors.New("TALKIE_OIDC_CLIENT_ID fehlt")
		}
	}
	if cfg.Mailer == nil && cfg.OIDC == nil {
		log.Printf("Hinweis: weder SMTP noch OIDC eingerichtet – auf der Verwaltungsseite kann sich niemand anmelden")
	}
	// Erster Admin: nur solange es noch keinen gibt
	if email := env("TALKIE_ADMIN_EMAIL", ""); email != "" {
		if has, err := store.HasAdmin(); err != nil {
			return nil, err
		} else if !has {
			if _, err := store.AddAccount(Account{Email: email, Name: "Admin", Role: RoleAdmin}); err != nil {
				return nil, fmt.Errorf("erster admin: %w", err)
			}
			log.Printf("Admin-Konto für %s angelegt", email)
		}
	}
	return newWebApp(hub, store, cfg)
}
