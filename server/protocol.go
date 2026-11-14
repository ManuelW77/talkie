package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	ProtocolVersion = 1
	ALPN            = "talkie/1"
	MaxFrameSize    = 64 * 1024
	AuthContext     = "talkie-auth-v1"

	DatagramVoice = 0x01
)

// Envelope ist die Obermenge aller Steuernachrichten (Feld "t" = Typ).
// Leere Felder werden beim Senden weggelassen.
type Envelope struct {
	T string `json:"t"`

	// hello / challenge / auth / register
	V      int    `json:"v,omitempty"`
	PubKey string `json:"pubkey,omitempty"`
	Nonce  string `json:"nonce,omitempty"`
	Known  *bool  `json:"known,omitempty"`
	Sig    string `json:"sig,omitempty"`
	Name   string `json:"name,omitempty"`
	Invite string `json:"invite,omitempty"`
	// register: Art des Geräts (z. B. „iOS“, „Windows“), nur zur Anzeige
	Device string `json:"device,omitempty"`

	// welcome / updates
	You      *WireUser   `json:"you,omitempty"`
	User     *WireUser   `json:"user,omitempty"`
	Users    []WireUser  `json:"users,omitempty"`
	Rooms    []DBRoom    `json:"rooms,omitempty"`
	ID       int64       `json:"id,omitempty"`
	Room     int64       `json:"room,omitempty"`
	Messages []DBMessage `json:"messages,omitempty"`
	Msg      *DBMessage  `json:"msg,omitempty"`

	// join / create_room: Raumpasswort (nie vom Server an Clients geschickt)
	Password string `json:"password,omitempty"`

	// chat / state
	Text  string `json:"text,omitempty"`
	Muted *bool  `json:"muted,omitempty"`
	Deaf  *bool  `json:"deaf,omitempty"`

	// ignore: Nutzer, die dieser Client für sich stumm geschaltet hat
	IDs []int64 `json:"ids,omitempty"`

	// welcome: darf rauswerfen/sperren (verknüpftes Webkonto mit Recht)
	Mod *bool `json:"mod,omitempty"`
	// welcome: verknüpftes Admin-Konto (darf u. a. den Chat leeren)
	Admin *bool `json:"admin,omitempty"`
	// welcome: so viele Stunden bleibt der Chat erhalten (0 = unbegrenzt)
	ChatHours int `json:"chat_hours,omitempty"`
	// welcome: neueste App-Version und wo es sie gibt
	Latest    string `json:"latest,omitempty"`
	LatestURL string `json:"latest_url,omitempty"`

	// idle_warning: so viele Sekunden bis zur Trennung wegen Inaktivität
	Secs int `json:"secs,omitempty"`

	// devices: eigene Geräte; zusammen mit code (offener Geräte-Code) und expires (Unix-Sekunden)
	Devices []WireDevice `json:"devices,omitempty"`
	Expires int64        `json:"expires,omitempty"`

	// set_avatar: Bild als base64 (JPEG/PNG, 128×128; leer = entfernen). avatar: Antwort auf get_avatar
	// (zusammen mit id und data), members: Antwort auf members
	Data    string   `json:"data,omitempty"`
	Avatar  int64    `json:"avatar,omitempty"`
	Members []Member `json:"members,omitempty"`

	// error / kicked
	Code   string `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// WireDevice ist ein Gerät des Nutzers in der Geräteliste der App.
type WireDevice struct {
	ID       int64  `json:"id"`
	Label    string `json:"label"`
	Created  int64  `json:"created"`
	LastSeen int64  `json:"last_seen"`
	This     bool   `json:"this"`
}

type WireUser struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Room  int64  `json:"room"`
	Muted bool   `json:"muted"`
	Deaf  bool   `json:"deaf"`
	// Avatar: Version des Profilbilds (0 = keins)
	Avatar int64 `json:"avatar,omitempty"`
}

var ErrFrameTooLarge = errors.New("nachricht zu groß")

func ReadFrame(r io.Reader) (*Envelope, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > MaxFrameSize {
		return nil, ErrFrameTooLarge
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	var e Envelope
	if err := json.Unmarshal(buf, &e); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	return &e, nil
}

func EncodeFrame(e *Envelope) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return nil, err
	}
	body := bytes.TrimRight(buf.Bytes(), "\n")
	if len(body) > MaxFrameSize {
		return nil, ErrFrameTooLarge
	}
	out := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(out, uint32(len(body)))
	copy(out[4:], body)
	return out, nil
}

func boolPtr(b bool) *bool { return &b }
