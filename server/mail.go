package main

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// Mailer verschickt Text-Mails (Login-Links, Einladungen).
type Mailer interface {
	Send(to, subject, body string) error
}

type SMTPConfig struct {
	Host, Port, User, Pass, From string
	Mode                         string // "starttls" (Standard), "tls" (Port 465) oder "none" (nur lokal, z. B. Mailpit)
}

func smtpFromEnv() *SMTPConfig {
	c := &SMTPConfig{
		Host: env("TALKIE_SMTP_HOST", ""),
		Port: env("TALKIE_SMTP_PORT", "587"),
		User: env("TALKIE_SMTP_USER", ""),
		Pass: env("TALKIE_SMTP_PASS", ""),
		From: env("TALKIE_SMTP_FROM", ""),
		Mode: strings.ToLower(env("TALKIE_SMTP_TLS", "")),
	}
	if c.Host == "" {
		return nil
	}
	if c.Mode == "" {
		c.Mode = "starttls"
		if c.Port == "465" {
			c.Mode = "tls"
		}
	}
	if c.From == "" {
		c.From = c.User
	}
	return c
}

type smtpMailer struct{ c SMTPConfig }

func (m *smtpMailer) Send(to, subject, body string) error {
	from, err := mail.ParseAddress(m.c.From)
	if err != nil {
		return fmt.Errorf("TALKIE_SMTP_FROM ungültig: %w", err)
	}
	rcpt, err := mail.ParseAddress(to)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(m.c.Host, m.c.Port)
	tlsConf := &tls.Config{ServerName: m.c.Host, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	d := &net.Dialer{Timeout: 15 * time.Second}
	if m.c.Mode == "tls" {
		conn, err = tls.DialWithDialer(d, "tcp", addr, tlsConf)
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	cl, err := smtp.NewClient(conn, m.c.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer cl.Close()
	if m.c.Mode == "starttls" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return errors.New("SMTP-Server kann kein STARTTLS (TALKIE_SMTP_TLS=tls oder none setzen)")
		}
		if err := cl.StartTLS(tlsConf); err != nil {
			return err
		}
	}
	if m.c.User != "" {
		if err := cl.Auth(smtp.PlainAuth("", m.c.User, m.c.Pass, m.c.Host)); err != nil {
			return err
		}
	}
	if err := cl.Mail(from.Address); err != nil {
		return err
	}
	if err := cl.Rcpt(rcpt.Address); err != nil {
		return err
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(buildMail(from, rcpt.Address, subject, body)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

func buildMail(from *mail.Address, to, subject, body string) []byte {
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := "talkie.local"
	if i := strings.LastIndex(from.Address, "@"); i >= 0 {
		domain = from.Address[i+1:]
	}
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from.String())
	h("To", to)
	h("Subject", mime.QEncoding.Encode("utf-8", subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("Content-Transfer-Encoding", "8bit")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	return []byte(b.String())
}
