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
