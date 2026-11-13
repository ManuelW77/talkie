package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

// mailIcon ist das Talkie-Icon, das als Inline-Bild (cid:talkie-icon) in den HTML-Mails steckt.
//
//go:embed web/mail/icon.png
var mailIcon []byte

// Mailer verschickt Mails (Login-Links, Einladungen). Ist html leer, geht nur der Text raus,
// sonst als multipart/alternative mit Text- und HTML-Fassung.
type Mailer interface {
	Send(to, subject, text, html string) error
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
	if c.Host == "" || strings.HasSuffix(c.Host, "example.org") {
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

func (m *smtpMailer) Send(to, subject, text, html string) error {
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
	if _, err := w.Write(buildMail(from, rcpt.Address, subject, text, html)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

func buildMail(from *mail.Address, to, subject, text, html string) []byte {
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := "talkie.local"
	if i := strings.LastIndex(from.Address, "@"); i >= 0 {
		domain = from.Address[i+1:]
	}
	crlf := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
	}
	var b bytes.Buffer
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from.String())
	h("To", to)
	h("Subject", mime.QEncoding.Encode("utf-8", subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	h("MIME-Version", "1.0")
	if html == "" {
		h("Content-Type", "text/plain; charset=utf-8")
		h("Content-Transfer-Encoding", "8bit")
		b.WriteString("\r\n")
		b.WriteString(crlf(text))
		return b.Bytes()
	}

	// Teil schreiben: Kopfzeilen + Inhalt (quoted-printable bricht Zeilen automatisch bei 76 Zeichen)
	qp := func(mw *multipart.Writer, ctype, content string) {
		pw, _ := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {ctype + "; charset=utf-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		w := quotedprintable.NewWriter(pw)
		_, _ = w.Write([]byte(crlf(content)))
		_ = w.Close()
	}
	alt := multipart.NewWriter(&b)
	h("Content-Type", "multipart/alternative; boundary="+alt.Boundary())
	b.WriteString("\r\n")
	qp(alt, "text/plain", text)

	var rel bytes.Buffer
	rw := multipart.NewWriter(&rel)
	qp(rw, "text/html", html)
	ip, _ := rw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"image/png; name=\"talkie.png\""},
		"Content-Transfer-Encoding": {"base64"},
		"Content-ID":                {"<talkie-icon>"},
		"Content-Disposition":       {"inline; filename=\"talkie.png\""},
	})
	enc := base64.StdEncoding.EncodeToString(mailIcon)
	for len(enc) > 76 {
		_, _ = ip.Write([]byte(enc[:76] + "\r\n"))
		enc = enc[76:]
	}
	_, _ = ip.Write([]byte(enc + "\r\n"))
	_ = rw.Close()

	rp, _ := alt.CreatePart(textproto.MIMEHeader{
		"Content-Type": {"multipart/related; type=\"text/html\"; boundary=" + rw.Boundary()},
	})
	_, _ = rp.Write(rel.Bytes())
	_ = alt.Close()
	return b.Bytes()
}
