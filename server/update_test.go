package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpdateCheckerGitHub(t *testing.T) {
	body := `{"tag_name":"v1.4.2","html_url":"https://github.com/x/talkie/releases/tag/v1.4.2","draft":false,"prerelease":false}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("ohne User-Agent lehnt GitHub ab")
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()
	u := NewUpdateChecker("", "", "x/talkie")
	u.APIURL = srv.URL
	if err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := u.Get(); got.Version != "1.4.2" || got.URL == "" {
		t.Fatalf("%+v", got)
	}
	// Fremder Link wird nicht weitergegeben
	body = `{"tag_name":"v1.4.3","html_url":"https://evil.example/x"}`
	u.Check(context.Background())
	if got := u.Get(); got.Version != "1.4.3" || got.URL != "" {
		t.Fatalf("fremder Link: %+v", got)
	}
	// Vorabversion und Unsinn werden ignoriert
	body = `{"tag_name":"v2.0.0","prerelease":true}`
	u.Check(context.Background())
	body = `{"tag_name":"<script>"}`
	u.Check(context.Background())
	if got := u.Get(); got.Version != "1.4.3" {
		t.Fatalf("überschrieben: %+v", got)
	}
}

func TestUpdateCheckerFixedAndOff(t *testing.T) {
	if got := NewUpdateChecker("v3.1.0", "https://example.org/talkie", "x/y").Get(); got.Version != "3.1.0" {
		t.Fatalf("fest: %+v", got)
	}
	off := NewUpdateChecker("", "", "")
	if off.APIURL != "" || off.Get().Version != "" {
		t.Fatal("ohne Repo muss die Prüfung aus sein")
	}
	var none *UpdateChecker
	_ = none.Get()
}
