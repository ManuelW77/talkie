package main

import (
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func urlq(s string) string { return url.QueryEscape(s) }

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (w *webApp) roomNames() map[int64]string {
	out := map[int64]string{}
	if rooms, err := w.store.Rooms(); err == nil {
		for _, r := range rooms {
			out[r.ID] = r.Name
		}
	}
	return out
}

// --- Übersicht ---

type onlineRow struct {
	Name, Room  string
	Muted, Deaf bool
}

func (w *webApp) dashboard(rw http.ResponseWriter, r *http.Request) {
	rooms := w.roomNames()
	var online []onlineRow
	for _, u := range w.hub.Online() {
		online = append(online, onlineRow{u.Name, rooms[u.Room], u.Muted, u.Deaf})
	}
	sort.Slice(online, func(i, j int) bool { return strings.ToLower(online[i].Name) < strings.ToLower(online[j].Name) })
	users, _ := w.store.Users()
	invites, _ := w.store.Invites()
	w.render(rw, r, "dashboard", "Übersicht", map[string]any{
		"Online":      online,
		"Users":       len(users),
		"Invites":     len(invites),
		"Rooms":       len(rooms),
		"Fingerprint": FormatFingerprint(w.hub.certHash),
		"ServerAddr":  w.cfg.ServerAddr,
	})
}

// --- Talkie-Nutzer ---

type userRow struct {
	DBUser
	Online bool
}

func (w *webApp) usersPage(rw http.ResponseWriter, r *http.Request) {
	users, err := w.store.Users()
	if err != nil {
		w.fail(rw, r, "/", err)
		return
	}
	online := w.hub.OnlineIDs()
	rows := make([]userRow, 0, len(users))
	for _, u := range users {
		rows = append(rows, userRow{u, online[u.ID]})
	}
	w.render(rw, r, "users", "Talkie-Nutzer", map[string]any{"Users": rows})
}

func (w *webApp) userPage(rw http.ResponseWriter, r *http.Request) {
	if a := me(r); !a.Can(PermUsersEdit) && !a.Can(PermUsersDelete) {
		rw.WriteHeader(http.StatusForbidden)
		w.render(rw, r, "forbidden", "Kein Zugriff", nil)
		return
	}
	id, ok := pathID(r)
	if !ok {
		http.NotFound(rw, r)
		return
	}
	u, err := w.store.UserByID(id)
	if err != nil {
		w.fail(rw, r, "/users", err)
		return
	}
	w.render(rw, r, "user", u.Name, map[string]any{"U": userRow{*u, w.hub.OnlineIDs()[u.ID]}})
}

func (w *webApp) userAction(rw http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(rw, r)
		return
	}
	action := r.PathValue("action")
	need := PermUsersEdit
	if action == "delete" {
		need = PermUsersDelete
	}
	if !me(r).Can(need) {
		rw.WriteHeader(http.StatusForbidden)
		w.render(rw, r, "forbidden", "Kein Zugriff", nil)
		return
	}
	u, err := w.store.UserByID(id)
	if err != nil {
		w.fail(rw, r, "/users", err)
		return
	}
	back := "/users/" + strconv.FormatInt(id, 10)
	switch action {
	case "rename":
		to, ok := cleanName(r.PostFormValue("name"))
		if !ok {
			w.done(rw, r, back, "Ungültiger Name: 2–20 Zeichen, nur Buchstaben, Ziffern, Leerzeichen, - _ .", true)
			return
		}
		nu, err := w.store.RenameUser(u.Name, to)
		if err != nil {
			w.fail(rw, r, back, err)
			return
		}
		w.hub.Renamed(nu.ID, nu.Name)
		w.audit(r, fmt.Sprintf("Talkie-Nutzer %s umbenannt in %s", u.Name, nu.Name))
		w.done(rw, r, back, "Umbenannt in "+nu.Name+".", false)
	case "kick":
		if !w.hub.Kick(u.ID, "Du wurdest vom Admin rausgeworfen.") {
			w.done(rw, r, back, u.Name+" ist nicht online.", true)
			return
		}
		w.audit(r, "Talkie-Nutzer "+u.Name+" rausgeworfen")
		w.done(rw, r, back, u.Name+" wurde rausgeworfen.", false)
	case "ban", "unban":
		ban := action == "ban"
		if _, err := w.store.SetBanned(u.Name, ban); err != nil {
			w.fail(rw, r, back, err)
			return
		}
		msg := u.Name + " ist wieder freigeschaltet."
		if ban {
			w.hub.Kick(u.ID, "Du wurdest vom Admin gesperrt.")
			msg = u.Name + " ist gesperrt."
			w.audit(r, "Talkie-Nutzer "+u.Name+" gesperrt")
		} else {
			w.audit(r, "Talkie-Nutzer "+u.Name+" entsperrt")
		}
		w.done(rw, r, back, msg, false)
	case "delete":
		if _, err := w.store.DeleteUser(u.Name); err != nil {
			w.fail(rw, r, back, err)
			return
		}
		w.hub.Kick(u.ID, "Dein Zugang wurde gelöscht.")
		w.audit(r, "Talkie-Nutzer "+u.Name+" gelöscht")
		w.done(rw, r, "/users", u.Name+" wurde entfernt.", false)
	default:
		http.NotFound(rw, r)
	}
}

// --- Einladungen ---

func (w *webApp) invitesPage(rw http.ResponseWriter, r *http.Request) {
	invs, err := w.store.Invites()
	if err != nil {
		w.fail(rw, r, "/", err)
		return
	}
	w.render(rw, r, "invites", "Einladungen", map[string]any{"Invites": invs, "New": r.URL.Query().Get("new")})
}

func formInt(r *http.Request, key string, def, min, max int) int {
	v, err := strconv.Atoi(r.PostFormValue(key))
	if err != nil || v < min || v > max {
		return def
	}
	return v
}

func (w *webApp) createInvite(rw http.ResponseWriter, r *http.Request) {
	uses := formInt(r, "uses", 1, 1, 100)
	days := formInt(r, "days", 7, 1, 365)
	email := strings.TrimSpace(r.PostFormValue("email"))
	if email != "" {
		if w.cfg.Mailer == nil {
			w.done(rw, r, "/invites", "Mailversand ist nicht eingerichtet.", true)
			return
		}
		if a, err := mail.ParseAddress(email); err != nil || a.Name != "" {
			w.done(rw, r, "/invites", "Die E-Mail-Adresse ist ungültig.", true)
			return
		}
	}
	inv, err := w.store.CreateInvite(uses, time.Duration(days)*24*time.Hour)
	if err != nil {
		w.fail(rw, r, "/invites", err)
		return
	}
	w.audit(r, fmt.Sprintf("Einladung %s erstellt (%d Person(en), %d Tage)", inv.Code, uses, days))
	msg := "Einladung erstellt: " + inv.Code
	if email != "" {
		if err := w.cfg.Mailer.Send(email, "Einladung zu Talkie", w.inviteMail(inv, me(r).Name)); err != nil {
			w.done(rw, r, "/invites?new="+urlq(inv.Code), msg+" – die Mail konnte aber nicht verschickt werden.", true)
			return
		}
		w.audit(r, "Einladung "+inv.Code+" per Mail an "+email+" geschickt")
		msg += " und an " + email + " geschickt."
	}
	w.done(rw, r, "/invites?new="+urlq(inv.Code), msg, false)
}

func (w *webApp) inviteMail(inv *DBInvite, from string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hallo!\n\n%s lädt dich zu Talkie ein, unserem Sprach-Chat zum Zocken.\n\n", from)
	step := 1
	if w.cfg.DownloadURL != "" {
		fmt.Fprintf(&b, "%d. App herunterladen: %s\n", step, w.cfg.DownloadURL)
		step++
	}
	fmt.Fprintf(&b, "%d. App starten und einen Namen ausdenken\n", step)
	step++
	if w.cfg.ServerAddr != "" {
		fmt.Fprintf(&b, "%d. Server (falls gefragt): %s\n", step, w.cfg.ServerAddr)
		step++
	}
	fmt.Fprintf(&b, "%d. Einladungscode eingeben:  %s\n\n", step, inv.Code)
	fmt.Fprintf(&b, "Der Code gilt bis %s.\n\nViel Spaß!\n", inv.Expires.Local().Format("02.01.2006"))
	return b.String()
}

func (w *webApp) deleteInvite(rw http.ResponseWriter, r *http.Request) {
	code := NormalizeInvite(r.PathValue("code"))
	if err := w.store.DeleteInvite(code); err != nil {
		w.fail(rw, r, "/invites", err)
		return
	}
	w.audit(r, "Einladung "+code+" gelöscht")
	w.done(rw, r, "/invites", "Einladung gelöscht.", false)
}

// --- Räume ---

type roomRow struct {
	DBRoom
	Online int
}

func (w *webApp) roomsPage(rw http.ResponseWriter, r *http.Request) {
	rooms, err := w.store.Rooms()
	if err != nil {
		w.fail(rw, r, "/", err)
		return
	}
	count := map[int64]int{}
	for _, u := range w.hub.Online() {
		count[u.Room]++
	}
	rows := make([]roomRow, 0, len(rooms))
	for _, rm := range rooms {
		rows = append(rows, roomRow{rm, count[rm.ID]})
	}
	w.render(rw, r, "rooms", "Räume", map[string]any{"Rooms": rows, "Last": len(rows) - 1})
}

const badRoomName = "Ungültiger Raumname: 2–20 Zeichen, nur Buchstaben, Ziffern, Leerzeichen, - _ ."

func (w *webApp) addRoom(rw http.ResponseWriter, r *http.Request) {
	name, ok := cleanName(r.PostFormValue("name"))
	if !ok {
		w.done(rw, r, "/rooms", badRoomName, true)
		return
	}
	if _, err := w.store.AddRoom(name); err != nil {
		w.fail(rw, r, "/rooms", err)
		return
	}
	if err := w.hub.ReloadRooms(); err != nil {
		w.fail(rw, r, "/rooms", err)
		return
	}
	w.audit(r, "Raum "+name+" angelegt")
	w.done(rw, r, "/rooms", "Raum „"+name+"“ angelegt.", false)
}

func (w *webApp) roomAction(rw http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(rw, r)
		return
	}
	room, err := w.store.RoomByID(id)
	if err != nil {
		w.fail(rw, r, "/rooms", err)
		return
	}
	var msg string
	switch r.PathValue("action") {
	case "rename":
		name, ok := cleanName(r.PostFormValue("name"))
		if !ok {
			w.done(rw, r, "/rooms", badRoomName, true)
			return
		}
		err = w.store.RenameRoom(id, name)
		msg = "Raum umbenannt in „" + name + "“."
		w.audit(r, "Raum "+room.Name+" umbenannt in "+name)
	case "up", "down":
		dir := 1
		if r.PathValue("action") == "up" {
			dir = -1
		}
		err = w.store.MoveRoom(id, dir)
	case "delete":
		rooms, lerr := w.store.Rooms()
		if lerr != nil {
			w.fail(rw, r, "/rooms", lerr)
			return
		}
		if len(rooms) <= 1 {
			w.done(rw, r, "/rooms", "Der letzte Raum kann nicht gelöscht werden.", true)
			return
		}
		err = w.store.DeleteRoom(id)
		msg = "Raum „" + room.Name + "“ gelöscht."
		w.audit(r, "Raum "+room.Name+" gelöscht")
	default:
		http.NotFound(rw, r)
		return
	}
	if err != nil {
		w.fail(rw, r, "/rooms", err)
		return
	}
	if err := w.hub.ReloadRooms(); err != nil {
		w.fail(rw, r, "/rooms", err)
		return
	}
	if msg == "" {
		http.Redirect(rw, r, "/rooms", http.StatusSeeOther)
		return
	}
	w.done(rw, r, "/rooms", msg, false)
}

// --- Chat mitlesen ---

func (w *webApp) chatPage(rw http.ResponseWriter, r *http.Request) {
	rooms, err := w.store.Rooms()
	if err != nil || len(rooms) == 0 {
		w.fail(rw, r, "/", err)
		return
	}
	sel := rooms[0]
	if id, err := strconv.ParseInt(r.URL.Query().Get("room"), 10, 64); err == nil {
		for _, rm := range rooms {
			if rm.ID == id {
				sel = rm
			}
		}
	}
	msgs, err := w.store.History(sel.ID, 200)
	if err != nil {
		w.fail(rw, r, "/", err)
		return
	}
	w.render(rw, r, "chat", "Chat", map[string]any{
		"Rooms": rooms, "Room": sel, "Messages": msgs, "KeepHours": int(w.store.ChatKeep / time.Hour),
	})
}

// chatClear leert den Chat eines Raums oder aller Räume (nur Admins).
func (w *webApp) chatClear(rw http.ResponseWriter, r *http.Request) {
	room, _ := strconv.ParseInt(r.PostFormValue("room"), 10, 64)
	back := "/chat"
	name := "allen Räumen"
	if room != 0 {
		rm, err := w.store.RoomByID(room)
		if err != nil {
			w.fail(rw, r, back, err)
			return
		}
		back += "?room=" + strconv.FormatInt(room, 10)
		name = "„" + rm.Name + "“"
	}
	n, err := w.hub.ClearChat(room)
	if err != nil {
		w.fail(rw, r, back, err)
		return
	}
	w.audit(r, "Chat in "+name+" geleert")
	w.done(rw, r, back, fmt.Sprintf("Chat in %s geleert (%d Nachrichten).", name, n), false)
}

// --- Web-Konten (nur Admin) ---

func (w *webApp) accountsPage(rw http.ResponseWriter, r *http.Request) {
	accs, err := w.store.Accounts()
	if err != nil {
		w.fail(rw, r, "/", err)
		return
	}
	names := map[int64]string{}
	if users, err := w.store.Users(); err == nil {
		for _, u := range users {
			names[u.ID] = u.Name
		}
	}
	w.render(rw, r, "accounts", "Web-Konten", map[string]any{"Accounts": accs, "TalkieNames": names, "Perms": permList})
}

func (w *webApp) accountForm(rw http.ResponseWriter, r *http.Request) {
	acc := &Account{Role: RoleUser, Perms: PermInvite}
	isNew := r.PathValue("id") == ""
	if !isNew {
		id, ok := pathID(r)
		if !ok {
			http.NotFound(rw, r)
			return
		}
		var err error
		if acc, err = w.store.AccountByID(id); err != nil {
			w.fail(rw, r, "/accounts", err)
			return
		}
	}
	users, _ := w.store.Users()
	title := "Neues Web-Konto"
	if !isNew {
		title = acc.Name
	}
	w.render(rw, r, "account", title, map[string]any{"A": acc, "New": isNew, "Perms": permList, "Users": users})
}

func cleanAccountName(n string) (string, bool) {
	n = strings.Join(strings.Fields(n), " ")
	l := utf8.RuneCountInString(n)
	if l < 1 || l > 40 {
		return "", false
	}
	for _, r := range n {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	return n, true
}

func (w *webApp) accountSave(rw http.ResponseWriter, r *http.Request) {
	isNew := r.PathValue("id") == ""
	back := "/accounts/new"
	var acc Account
	if !isNew {
		id, ok := pathID(r)
		if !ok {
			http.NotFound(rw, r)
			return
		}
		back = "/accounts/" + strconv.FormatInt(id, 10)
		old, err := w.store.AccountByID(id)
		if err != nil {
			w.fail(rw, r, "/accounts", err)
			return
		}
		acc = *old
	}
	email := normEmail(r.PostFormValue("email"))
	if a, err := mail.ParseAddress(email); err != nil || a.Name != "" || a.Address != email {
		w.done(rw, r, back, "Die E-Mail-Adresse ist ungültig.", true)
		return
	}
	name, ok := cleanAccountName(r.PostFormValue("name"))
	if !ok {
		w.done(rw, r, back, "Bitte einen Namen angeben (bis 40 Zeichen).", true)
		return
	}
	role := r.PostFormValue("role")
	if role != RoleAdmin {
		role = RoleUser
	}
	var perms Perm
	for _, p := range permList {
		if r.PostFormValue("perm_"+p.Key) == "1" {
			perms |= p.P
		}
	}
	talkieID, _ := strconv.ParseInt(r.PostFormValue("talkie_id"), 10, 64)
	if talkieID != 0 {
		if _, err := w.store.UserByID(talkieID); err != nil {
			talkieID = 0
		}
	}
	emailChanged := !isNew && acc.Email != email
	acc.Email, acc.Name, acc.Role, acc.Perms, acc.TalkieID = email, name, role, perms, talkieID
	acc.Disabled = r.PostFormValue("disabled") == "1"
	if !isNew && acc.ID == me(r).ID && acc.Disabled {
		w.done(rw, r, back, "Du kannst dein eigenes Konto nicht deaktivieren.", true)
		return
	}

	if isNew {
		created, err := w.store.AddAccount(acc)
		if err != nil {
			if err == ErrNameTaken {
				err = userError("Für diese E-Mail-Adresse gibt es schon ein Konto.")
			}
			w.fail(rw, r, back, err)
			return
		}
		w.audit(r, fmt.Sprintf("Web-Konto %s (%s) angelegt", created.Email, roleLabel(role)))
		w.done(rw, r, "/accounts", "Konto für "+created.Email+" angelegt. Die Anmeldung läuft per Mail-Code"+w.oidcHint()+".", false)
		return
	}
	if err := w.store.UpdateAccount(acc); err != nil {
		if err == ErrNameTaken {
			err = userError("Für diese E-Mail-Adresse gibt es schon ein Konto.")
		}
		w.fail(rw, r, back, err)
		return
	}
	// Neue Adresse oder ausdrücklich gewünscht: OIDC-Verknüpfung lösen
	if emailChanged || r.PostFormValue("reset_oidc") == "1" {
		_ = w.store.SetOIDCSub(acc.ID, "")
	}
	w.audit(r, fmt.Sprintf("Web-Konto %s geändert (%s)", acc.Email, roleLabel(role)))
	w.done(rw, r, "/accounts", "Konto gespeichert.", false)
}

func (w *webApp) oidcHint() string {
	if w.cfg.OIDC != nil {
		return " oder " + w.cfg.OIDC.Name
	}
	return ""
}

func roleLabel(role string) string {
	if role == RoleAdmin {
		return "Admin"
	}
	return "Benutzer"
}

func (w *webApp) accountDelete(rw http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(rw, r)
		return
	}
	if id == me(r).ID {
		w.done(rw, r, "/accounts", "Du kannst dein eigenes Konto nicht löschen.", true)
		return
	}
	acc, err := w.store.AccountByID(id)
	if err != nil {
		w.fail(rw, r, "/accounts", err)
		return
	}
	if err := w.store.DeleteAccount(id); err != nil {
		w.fail(rw, r, "/accounts", err)
		return
	}
	w.audit(r, "Web-Konto "+acc.Email+" gelöscht")
	w.done(rw, r, "/accounts", "Konto "+acc.Email+" gelöscht.", false)
}

func (w *webApp) auditPage(rw http.ResponseWriter, r *http.Request) {
	log, err := w.store.AuditLog(300)
	if err != nil {
		w.fail(rw, r, "/", err)
		return
	}
	w.render(rw, r, "audit", "Protokoll", map[string]any{"Log": log})
}
