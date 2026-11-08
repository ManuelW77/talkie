package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// UpdateInfo ist die neueste App-Version, die den Apps angezeigt wird.
type UpdateInfo struct {
	Version string // z. B. "1.2.0" (ohne „v“)
	URL     string // wo es sie gibt
}

// UpdateChecker kennt die neueste App-Version: fest vorgegeben (TALKIE_LATEST_VERSION)
// oder einmal täglich vom GitHub-Release des Projekts. So fragen nur der Server und nie
// die Geräte der Kinder bei GitHub an.
type UpdateChecker struct {
	mu   sync.RWMutex
	info UpdateInfo
	// APIURL ist die GitHub-API-Adresse für das neueste Release (leer = aus).
	APIURL string
	client *http.Client
}

func NewUpdateChecker(fixedVersion, fixedURL, repo string) *UpdateChecker {
	u := &UpdateChecker{client: &http.Client{Timeout: 10 * time.Second}}
	if v := strings.TrimPrefix(strings.TrimSpace(fixedVersion), "v"); v != "" {
		u.info = UpdateInfo{Version: v, URL: fixedURL}
		return u
	}
	if repo = strings.Trim(strings.TrimSpace(repo), "/"); repo != "" {
		u.APIURL = "https://api.github.com/repos/" + repo + "/releases/latest"
	}
	return u
}

func (u *UpdateChecker) Get() UpdateInfo {
	if u == nil {
		return UpdateInfo{}
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.info
}

// Run fragt sofort und danach alle 24 Stunden bei GitHub nach (nur ohne feste Vorgabe).
func (u *UpdateChecker) Run(ctx context.Context) {
	if u.APIURL == "" {
		return
	}
	for {
		if err := u.Check(ctx); err != nil {
			log.Printf("versionsprüfung: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}

func (u *UpdateChecker) Check(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u.APIURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "talkie-server")
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil // noch kein Release
	}
	if resp.StatusCode != http.StatusOK {
		return &httpStatusError{resp.StatusCode}
	}
	var rel struct {
		Tag        string `json:"tag_name"`
		URL        string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&rel); err != nil {
		return err
	}
	v := strings.TrimPrefix(rel.Tag, "v")
	if v == "" || rel.Draft || rel.Prerelease || !validVersion(v) {
		return nil
	}
	u.mu.Lock()
	u.info = UpdateInfo{Version: v, URL: rel.URL}
	u.mu.Unlock()
	return nil
}

type httpStatusError struct{ code int }

func (e *httpStatusError) Error() string { return "github antwortet " + http.StatusText(e.code) }

// validVersion akzeptiert nur Zahlen mit Punkten (1.2.3), damit nichts Seltsames in die Apps gelangt.
func validVersion(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 4 || len(v) > 20 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}
