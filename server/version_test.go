package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestServerVersionMatchesPubspec(t *testing.T) {
	data, err := os.ReadFile("../app/pubspec.yaml")
	if err != nil {
		t.Skip("pubspec.yaml nicht vorhanden")
	}
	m := regexp.MustCompile(`(?m)^version:\s*(\d+\.\d+\.\d+)`).FindSubmatch(data)
	if m == nil {
		t.Fatal("keine Version in pubspec.yaml gefunden")
	}
	if string(m[1]) != ServerVersion {
		t.Fatalf("ServerVersion %s != pubspec %s", ServerVersion, m[1])
	}
}

func TestFooterOnLoginPage(t *testing.T) {
	wt := newWebTest(t, nil)
	_, _, body := wt.browser().get("/login")
	for _, want := range []string{"Talkie " + ServerVersion, "Manuel Weiser</a> in Leipzig", "https://manuelw.de", "github.com/ManuelW77/talkie"} {
		if !strings.Contains(body, want) {
			t.Errorf("Footer fehlt: %q", want)
		}
	}
}
