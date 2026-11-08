package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// Admin-API: lauscht nur auf localhost (im Container) und verlangt ein Token
// aus dataDir/admin.token. Die CLI-Befehle derselben Binary sprechen damit.

func loadOrCreateToken(dataDir string) (string, error) {
	p := filepath.Join(dataDir, "admin.token")
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) >= 32 {
		return strings.TrimSpace(string(b)), nil
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	return tok, os.WriteFile(p, []byte(tok+"\n"), 0o600)
}

type adminAPI struct {
	hub   *Hub
	store *Store
	token string
}

func (a *adminAPI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", a.status)
	mux.HandleFunc("POST /invites", a.createInvite)
	mux.HandleFunc("GET /invites", a.listInvites)
	mux.HandleFunc("DELETE /invites/{code}", a.deleteInvite)
	mux.HandleFunc("GET /users", a.listUsers)
	mux.HandleFunc("POST /users/{name}/{action}", a.userAction)
	mux.HandleFunc("DELETE /users/{name}", a.deleteUser)
	mux.HandleFunc("GET /rooms", a.listRooms)
	mux.HandleFunc("POST /rooms", a.addRoom)
	mux.HandleFunc("DELETE /rooms/{name}", a.deleteRoom)
	mux.HandleFunc("GET /chat", a.chatLog)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) != 1 {
			http.Error(w, "nicht erlaubt", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, ErrNameTaken):
		code = http.StatusConflict
	}
	http.Error(w, err.Error(), code)
}

func (a *adminAPI) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"fingerprint": FormatFingerprint(a.hub.certHash),
		"online":      a.hub.OnlineCount(),
	})
}

func (a *adminAPI) createInvite(w http.ResponseWriter, r *http.Request) {
	uses, _ := strconv.Atoi(r.URL.Query().Get("uses"))
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if uses < 1 || uses > 100 {
		uses = 1
	}
	if days < 1 || days > 365 {
		days = 7
	}
	inv, err := a.store.CreateInvite(uses, time.Duration(days)*24*time.Hour)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, inv)
}

func (a *adminAPI) listInvites(w http.ResponseWriter, _ *http.Request) {
	inv, err := a.store.Invites()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, inv)
}

func (a *adminAPI) deleteInvite(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteInvite(r.PathValue("code")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

type adminUser struct {
	Name     string    `json:"name"`
	Online   bool      `json:"online"`
	Banned   bool      `json:"banned"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"last_seen"`
}

func (a *adminAPI) listUsers(w http.ResponseWriter, _ *http.Request) {
	users, err := a.store.Users()
	if err != nil {
		writeErr(w, err)
		return
	}
	online := a.hub.OnlineIDs()
	out := []adminUser{}
	for _, u := range users {
		out = append(out, adminUser{u.Name, online[u.ID], u.Banned, u.Created, u.LastSeen})
	}
	writeJSON(w, out)
}

func (a *adminAPI) userAction(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var err error
	var u *DBUser
	switch r.PathValue("action") {
	case "ban":
		if u, err = a.store.SetBanned(name, true); err == nil {
			a.hub.Kick(u.ID, "Du wurdest vom Admin gesperrt.")
		}
	case "unban":
		u, err = a.store.SetBanned(name, false)
	case "kick":
		if u, err = a.store.UserByName(name); err == nil && !a.hub.Kick(u.ID, "Du wurdest vom Admin rausgeworfen.") {
			err = fmt.Errorf("%s ist nicht online: %w", name, ErrNotFound)
		}
	case "rename":
		to, ok := cleanName(r.URL.Query().Get("to"))
		if !ok {
			http.Error(w, "ungültiger Name", http.StatusBadRequest)
			return
		}
		if u, err = a.store.RenameUser(name, to); err == nil {
			a.hub.Renamed(u.ID, u.Name)
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"name": u.Name})
}

func (a *adminAPI) deleteUser(w http.ResponseWriter, r *http.Request) {
	u, err := a.store.DeleteUser(r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	a.hub.Kick(u.ID, "Dein Zugang wurde gelöscht.")
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *adminAPI) listRooms(w http.ResponseWriter, _ *http.Request) {
	rooms, err := a.store.Rooms()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, rooms)
}

func (a *adminAPI) addRoom(w http.ResponseWriter, r *http.Request) {
	name, ok := cleanName(r.URL.Query().Get("name"))
	if !ok {
		http.Error(w, "ungültiger Raumname (2–20 Zeichen)", http.StatusBadRequest)
		return
	}
	room, err := a.store.AddRoom(name)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := a.hub.ReloadRooms(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, room)
}

func (a *adminAPI) deleteRoom(w http.ResponseWriter, r *http.Request) {
	rooms, err := a.store.Rooms()
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(rooms) <= 1 {
		http.Error(w, "der letzte Raum kann nicht gelöscht werden", http.StatusBadRequest)
		return
	}
	room, err := a.store.RoomByName(r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := a.store.DeleteRoom(room.ID); err != nil {
		writeErr(w, err)
		return
	}
	if err := a.hub.ReloadRooms(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *adminAPI) chatLog(w http.ResponseWriter, r *http.Request) {
	room, err := a.store.RoomByName(r.URL.Query().Get("room"))
	if err != nil {
		writeErr(w, err)
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n < 1 || n > 200 {
		n = 50
	}
	msgs, err := a.store.History(room.ID, n)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, msgs)
}

// --- CLI ---

type adminClient struct {
	base  string
	token string
}

func (c *adminClient) do(method, path string, q url.Values, out any) error {
	u := "http://" + c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("Server nicht erreichbar (läuft er?): %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", strings.TrimSpace(string(body)))
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

const usage = `Talkie-Server

  talkie-server                     Server starten (Standard)
  talkie-server serve               Server starten

Verwaltung (während der Server läuft, z. B. per "docker exec talkie talkie-server ..."):
  status                            Fingerabdruck und Anzahl Online
  invite [anzahl] [tage]            Einladungscode erzeugen (Standard: 1 Person, 7 Tage)
  invites                           offene Einladungen anzeigen
  invite-del CODE                   Einladung löschen
  users                             alle Nutzer anzeigen
  kick NAME                         Nutzer rauswerfen (darf wiederkommen)
  ban NAME / unban NAME             Nutzer sperren / entsperren
  rename NAME NEUERNAME             Nutzer umbenennen
  user-del NAME                     Nutzer löschen (braucht neue Einladung)
  rooms                             Räume anzeigen
  room-add NAME / room-del NAME     Raum anlegen / löschen
  chat RAUM [anzahl]                letzte Chatnachrichten eines Raums
`

func runCLI(args []string, dataDir, adminAddr string) error {
	tok, err := os.ReadFile(filepath.Join(dataDir, "admin.token"))
	if err != nil {
		return fmt.Errorf("admin.token nicht lesbar (%v) — läuft der Server mit TALKIE_DATA=%s?", err, dataDir)
	}
	c := &adminClient{base: adminAddr, token: strings.TrimSpace(string(tok))}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	defer tw.Flush()
	need := func(n int) error {
		if len(args) < n+1 {
			return fmt.Errorf("zu wenige Angaben.\n\n%s", usage)
		}
		return nil
	}
	ok := func(msg string) { fmt.Fprintln(tw, msg) }
	esc := url.PathEscape

	switch args[0] {
	case "status":
		var st struct {
			Fingerprint string `json:"fingerprint"`
			Online      int    `json:"online"`
		}
		if err := c.do("GET", "/status", nil, &st); err != nil {
			return err
		}
		fmt.Fprintf(tw, "Online:\t%d\nFingerabdruck:\t%s\n", st.Online, st.Fingerprint)
	case "invite":
		q := url.Values{"uses": {"1"}, "days": {"7"}}
		if len(args) > 1 {
			q.Set("uses", args[1])
		}
		if len(args) > 2 {
			q.Set("days", args[2])
		}
		var inv DBInvite
		if err := c.do("POST", "/invites", q, &inv); err != nil {
			return err
		}
		fmt.Fprintf(tw, "Einladungscode:\t%s\nGilt für:\t%d Person(en)\nGültig bis:\t%s\n",
			inv.Code, inv.UsesLeft, inv.Expires.Format("02.01.2006 15:04"))
	case "invites":
		var invs []DBInvite
		if err := c.do("GET", "/invites", nil, &invs); err != nil {
			return err
		}
		fmt.Fprintln(tw, "CODE\tRESTLICH\tGÜLTIG BIS")
		for _, i := range invs {
			fmt.Fprintf(tw, "%s\t%d\t%s\n", i.Code, i.UsesLeft, i.Expires.Format("02.01.2006 15:04"))
		}
	case "invite-del":
		if err := need(1); err != nil {
			return err
		}
		if err := c.do("DELETE", "/invites/"+esc(args[1]), nil, nil); err != nil {
			return err
		}
		ok("Einladung gelöscht.")
	case "users":
		var us []adminUser
		if err := c.do("GET", "/users", nil, &us); err != nil {
			return err
		}
		fmt.Fprintln(tw, "NAME\tSTATUS\tZULETZT DA\tSEIT")
		for _, u := range us {
			st := "offline"
			if u.Online {
				st = "ONLINE"
			}
			if u.Banned {
				st = "GESPERRT"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", u.Name, st, u.LastSeen.Format("02.01.2006 15:04"), u.Created.Format("02.01.2006"))
		}
	case "kick", "ban", "unban":
		if err := need(1); err != nil {
			return err
		}
		if err := c.do("POST", "/users/"+esc(args[1])+"/"+args[0], nil, nil); err != nil {
			return err
		}
		ok(map[string]string{"kick": "Rausgeworfen: ", "ban": "Gesperrt: ", "unban": "Entsperrt: "}[args[0]] + args[1])
	case "rename":
		if err := need(2); err != nil {
			return err
		}
		if err := c.do("POST", "/users/"+esc(args[1])+"/rename", url.Values{"to": {args[2]}}, nil); err != nil {
			return err
		}
		ok("Umbenannt.")
	case "user-del":
		if err := need(1); err != nil {
			return err
		}
		if err := c.do("DELETE", "/users/"+esc(args[1]), nil, nil); err != nil {
			return err
		}
		ok("Nutzer gelöscht: " + args[1])
	case "rooms":
		var rs []DBRoom
		if err := c.do("GET", "/rooms", nil, &rs); err != nil {
			return err
		}
		for _, r := range rs {
			fmt.Fprintln(tw, r.Name)
		}
	case "room-add":
		if err := need(1); err != nil {
			return err
		}
		if err := c.do("POST", "/rooms", url.Values{"name": {strings.Join(args[1:], " ")}}, nil); err != nil {
			return err
		}
		ok("Raum angelegt.")
	case "room-del":
		if err := need(1); err != nil {
			return err
		}
		if err := c.do("DELETE", "/rooms/"+esc(strings.Join(args[1:], " ")), nil, nil); err != nil {
			return err
		}
		ok("Raum gelöscht.")
	case "chat":
		if err := need(1); err != nil {
			return err
		}
		q := url.Values{"room": {args[1]}}
		if len(args) > 2 {
			q.Set("n", args[2])
		}
		var ms []DBMessage
		if err := c.do("GET", "/chat", q, &ms); err != nil {
			return err
		}
		for _, m := range ms {
			fmt.Fprintf(tw, "%s\t%s:\t%s\n", time.Unix(m.TS, 0).Format("02.01. 15:04"), m.Name, m.Text)
		}
	default:
		fmt.Print(usage)
	}
	return nil
}
