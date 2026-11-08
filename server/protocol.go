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

	// welcome / updates
	You      *WireUser   `json:"you,omitempty"`
	User     *WireUser   `json:"user,omitempty"`
	Users    []WireUser  `json:"users,omitempty"`
	Rooms    []DBRoom    `json:"rooms,omitempty"`
	ID       int64       `json:"id,omitempty"`
	Room     int64       `json:"room,omitempty"`
	Messages []DBMessage `json:"messages,omitempty"`
	Msg      *DBMessage  `json:"msg,omitempty"`

	// chat / state
	Text  string `json:"text,omitempty"`
	Muted *bool  `json:"muted,omitempty"`
	Deaf  *bool  `json:"deaf,omitempty"`

	// ignore: Nutzer, die dieser Client für sich stumm geschaltet hat
	IDs []int64 `json:"ids,omitempty"`

	// error / kicked
	Code   string `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type WireUser struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Room  int64  `json:"room"`
	Muted bool   `json:"muted"`
	Deaf  bool   `json:"deaf"`
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
