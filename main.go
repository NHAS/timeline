// Story Timeline: a small web app for planning when things happen in your stories.
// Data lives in a single SQLite file; the UI is server-rendered HTML.
package main

import (
	"bytes"
	"database/sql"
	"embed"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, no CGO needed
)

//go:embed templates/*.html static/*
var assets embed.FS

const schema = `
CREATE TABLE IF NOT EXISTS stories (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	title       TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	mode        TEXT NOT NULL DEFAULT 'date',   -- 'date' (calendar) or 'number' (e.g. Day 12)
	unit        TEXT NOT NULL DEFAULT '',       -- label for number mode: Day, Year, Chapter...
	created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS characters (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	story_id INTEGER NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
	name     TEXT NOT NULL,
	color    TEXT NOT NULL DEFAULT '#6c8ebf'
);
CREATE TABLE IF NOT EXISTS events (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	story_id  INTEGER NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
	character_id  INTEGER REFERENCES characters(id) ON DELETE SET NULL,
	title     TEXT NOT NULL,
	notes     TEXT NOT NULL DEFAULT '',
	starts_at TEXT NOT NULL,
	ends_at   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_characters_story ON characters(story_id);
CREATE INDEX IF NOT EXISTS idx_events_story ON events(story_id);
`

const neutralColor = "#8d99ae"

var (
	palette  = []string{"#e76f51", "#2a9d8f", "#e9c46a", "#6c8ebf", "#9b5de5", "#f15bb5", "#00a6a6", "#8d99ae"}
	colorRe  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	pages    = map[string]*template.Template{}
	pageList = []string{"index", "story", "event_form"}
)

// ---------- models ----------

type Story struct {
	ID          int64
	Title       string
	Description string
	Mode        string
	Unit        string
	EventCount  int
}

func (s Story) IsDate() bool { return s.Mode != "number" }

type Characters struct {
	ID      int64
	StoryID int64
	Name    string
	Color   string
	Count   int
}

type Event struct {
	ID          int64
	StoryID     int64
	CharacterID sql.NullInt64
	Title       string
	Notes       string
	StartsAt    string
	EndsAt      string

	// filled in by decorate()
	key           float64
	CharacterName string
	Color         string
	Start         string
	When          string
}

// ---------- time handling ----------

// parseKey turns a stored time string into a sortable number.
func parseKey(mode, s string) (float64, error) {
	s = strings.TrimSpace(s)
	if mode == "number" {
		return strconv.ParseFloat(s, 64)
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return float64(t.Unix()), nil
		}
	}
	return 0, fmt.Errorf("%q is not a valid date", s)
}

func formatTime(st Story, s string) string {
	if st.Mode == "number" {
		return strings.TrimSpace(st.Unit + " " + s)
	}
	if t, err := time.Parse("2006-01-02 15:04", s); err == nil {
		return t.Format("2 Jan 2006, 15:04")
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Format("2 Jan 2006")
	}
	return s
}

func joinTime(mode, date, clock string) string {
	if mode == "date" && date != "" && clock != "" {
		return date + " " + clock
	}
	return date
}

func decorate(st Story, e *Event, tm map[int64]Characters) {
	if e.CharacterID.Valid {
		if t, ok := tm[e.CharacterID.Int64]; ok {
			e.CharacterName, e.Color = t.Name, t.Color
		}
	}
	if e.Color == "" {
		e.Color = neutralColor
	}
	e.Start = formatTime(st, e.StartsAt)
	e.When = e.Start
	if e.EndsAt != "" {
		e.When += " → " + formatTime(st, e.EndsAt)
	}
	e.key, _ = parseKey(st.Mode, e.StartsAt)
}

// ---------- app + data access ----------

type App struct{ db *sql.DB }

func openDB(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // single-user app; keeps SQLite happy
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return db, nil
}

func (a *App) listStories() ([]Story, error) {
	rows, err := a.db.Query(`SELECT s.id, s.title, s.description, s.mode, s.unit,
		(SELECT COUNT(*) FROM events e WHERE e.story_id = s.id)
		FROM stories s ORDER BY s.created_at DESC, s.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Story
	for rows.Next() {
		var s Story
		if err := rows.Scan(&s.ID, &s.Title, &s.Description, &s.Mode, &s.Unit, &s.EventCount); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (a *App) getStory(id int64) (Story, error) {
	var s Story
	err := a.db.QueryRow(`SELECT id, title, description, mode, unit FROM stories WHERE id = ?`, id).
		Scan(&s.ID, &s.Title, &s.Description, &s.Mode, &s.Unit)
	return s, err
}

func (a *App) listCharacters(storyID int64) ([]Characters, error) {
	rows, err := a.db.Query(`SELECT t.id, t.story_id, t.name, t.color,
		(SELECT COUNT(*) FROM events e WHERE e.character_id = t.id)
		FROM characters t WHERE t.story_id = ? ORDER BY t.id`, storyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Characters
	for rows.Next() {
		var t Characters
		if err := rows.Scan(&t.ID, &t.StoryID, &t.Name, &t.Color, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (a *App) listEvents(st Story, characters []Characters) ([]Event, error) {
	rows, err := a.db.Query(`SELECT id, story_id, character_id, title, notes, starts_at, ends_at
		FROM events WHERE story_id = ?`, st.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tm := map[int64]Characters{}
	for _, t := range characters {
		tm[t.ID] = t
	}
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.StoryID, &e.CharacterID, &e.Title, &e.Notes, &e.StartsAt, &e.EndsAt); err != nil {
			return nil, err
		}
		decorate(st, &e, tm)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].key != out[j].key {
			return out[i].key < out[j].key
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (a *App) getEvent(id int64) (Event, error) {
	var e Event
	err := a.db.QueryRow(`SELECT id, story_id, character_id, title, notes, starts_at, ends_at FROM events WHERE id = ?`, id).
		Scan(&e.ID, &e.StoryID, &e.CharacterID, &e.Title, &e.Notes, &e.StartsAt, &e.EndsAt)
	return e, err
}

// ---------- helpers ----------

func loadTemplates() {
	for _, name := range pageList {
		pages[name] = template.Must(template.New("base.html").
			ParseFS(assets, "templates/base.html", "templates/"+name+".html"))
	}
}

func (a *App) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := pages[name].ExecuteTemplate(&buf, "base.html", data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (a *App) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	http.Error(w, "something went wrong", http.StatusInternalServerError)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// ---------- story handlers ----------

func (a *App) index(w http.ResponseWriter, r *http.Request) {
	stories, err := a.listStories()
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.render(w, 200, "index", map[string]any{"Stories": stories})
}

func (a *App) createStory(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		redirect(w, r, "/")
		return
	}
	mode := "date"
	unit := ""
	if r.FormValue("mode") == "number" {
		mode = "number"
		unit = strings.TrimSpace(r.FormValue("unit"))
		if unit == "" {
			unit = "Day"
		}
	}
	res, err := a.db.Exec(`INSERT INTO stories (title, description, mode, unit) VALUES (?, ?, ?, ?)`,
		title, strings.TrimSpace(r.FormValue("description")), mode, unit)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	id, _ := res.LastInsertId()
	redirect(w, r, fmt.Sprintf("/stories/%d", id))
}

func (a *App) updateStory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	st, err := a.getStory(id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = st.Title
	}
	unit := st.Unit
	if st.Mode == "number" {
		if u := strings.TrimSpace(r.FormValue("unit")); u != "" {
			unit = u
		}
	}
	if _, err := a.db.Exec(`UPDATE stories SET title = ?, description = ?, unit = ? WHERE id = ?`,
		title, strings.TrimSpace(r.FormValue("description")), unit, id); err != nil {
		a.fail(w, r, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/stories/%d", id))
}

func (a *App) deleteStory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := a.db.Exec(`DELETE FROM stories WHERE id = ?`, id); err != nil {
		a.fail(w, r, err)
		return
	}
	redirect(w, r, "/")
}

// ---------- story page (list + swimlane views) ----------

type Chip struct {
	Label, Href, Color string
	Active             bool
}

type LaneCol struct{ Name, Color string }

type LaneRow struct {
	When  string
	Cells [][]Event
}

type StoryPage struct {
	Story          Story
	Characters     []Characters
	Events         []Event
	Total          int
	View           string
	ViewChips      []Chip
	CharacterChips []Chip
	Cols           []LaneCol
	Rows           []LaneRow
	NextColor      string
}

func filterEvents(evs []Event, filter string) []Event {
	if filter == "" {
		return evs
	}
	var out []Event
	for _, e := range evs {
		switch {
		case filter == "none" && !e.CharacterID.Valid:
			out = append(out, e)
		case e.CharacterID.Valid && strconv.FormatInt(e.CharacterID.Int64, 10) == filter:
			out = append(out, e)
		}
	}
	return out
}

func storyHref(id int64, view, filter string) string {
	q := url.Values{}
	if view == "lanes" {
		q.Set("view", "lanes")
	}
	if filter != "" {
		q.Set("character", filter)
	}
	href := fmt.Sprintf("/stories/%d", id)
	if len(q) > 0 {
		href += "?" + q.Encode()
	}
	return href
}

func buildLanes(characters []Characters, evs []Event, filter string) ([]LaneCol, []LaneRow) {
	var cols []LaneCol
	idx := map[int64]int{}
	for _, t := range characters {
		if filter == "" || filter == strconv.FormatInt(t.ID, 10) {
			idx[t.ID] = len(cols)
			cols = append(cols, LaneCol{Name: t.Name, Color: t.Color})
		}
	}
	unassigned := -1
	for _, e := range evs {
		if !e.CharacterID.Valid {
			unassigned = len(cols)
			cols = append(cols, LaneCol{Name: "Unassigned", Color: neutralColor})
			break
		}
	}
	var rows []LaneRow
	var last float64
	for i, e := range evs {
		if i == 0 || e.key != last {
			rows = append(rows, LaneRow{When: e.Start, Cells: make([][]Event, len(cols))})
			last = e.key
		}
		c := unassigned
		if e.CharacterID.Valid {
			c = idx[e.CharacterID.Int64]
		}
		row := &rows[len(rows)-1]
		row.Cells[c] = append(row.Cells[c], e)
	}
	return cols, rows
}

func (a *App) storyPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	st, err := a.getStory(id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	characters, err := a.listCharacters(id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	all, err := a.listEvents(st, characters)
	if err != nil {
		a.fail(w, r, err)
		return
	}

	view := "list"
	if r.URL.Query().Get("view") == "lanes" {
		view = "lanes"
	}
	filter := r.URL.Query().Get("character")
	if _, err := strconv.ParseInt(filter, 10, 64); err != nil && filter != "none" {
		filter = ""
	}
	shown := filterEvents(all, filter)

	p := StoryPage{
		Story: st, Characters: characters, Events: shown, Total: len(all), View: view,
		NextColor: palette[len(characters)%len(palette)],
	}
	p.ViewChips = []Chip{
		{Label: "Timeline", Href: storyHref(id, "list", filter), Active: view == "list"},
		{Label: "Swimlanes", Href: storyHref(id, "lanes", filter), Active: view == "lanes"},
	}
	p.CharacterChips = []Chip{{Label: "All characters", Href: storyHref(id, view, ""), Active: filter == ""}}
	for _, t := range characters {
		f := strconv.FormatInt(t.ID, 10)
		p.CharacterChips = append(p.CharacterChips, Chip{Label: t.Name, Color: t.Color, Href: storyHref(id, view, f), Active: filter == f})
	}
	for _, e := range all {
		if !e.CharacterID.Valid {
			p.CharacterChips = append(p.CharacterChips, Chip{Label: "Unassigned", Color: neutralColor, Href: storyHref(id, view, "none"), Active: filter == "none"})
			break
		}
	}
	if view == "lanes" {
		p.Cols, p.Rows = buildLanes(characters, shown, filter)
	}
	a.render(w, 200, "story", p)
}

// ---------- character handlers ----------

func cleanColor(c string, fallback string) string {
	c = strings.TrimSpace(c)
	if colorRe.MatchString(c) {
		return strings.ToLower(c)
	}
	return fallback
}

func (a *App) createCharacter(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := a.getStory(id); err != nil {
		a.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name != "" {
		var n int
		_ = a.db.QueryRow(`SELECT COUNT(*) FROM characters WHERE story_id = ?`, id).Scan(&n)
		color := cleanColor(r.FormValue("color"), palette[n%len(palette)])
		if _, err := a.db.Exec(`INSERT INTO characters (story_id, name, color) VALUES (?, ?, ?)`, id, name, color); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	redirect(w, r, fmt.Sprintf("/stories/%d", id))
}

func (a *App) updateCharacter(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var storyID int64
	var oldName, oldColor string
	if err := a.db.QueryRow(`SELECT story_id, name, color FROM characters WHERE id = ?`, id).Scan(&storyID, &oldName, &oldColor); err != nil {
		a.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = oldName
	}
	if _, err := a.db.Exec(`UPDATE characters SET name = ?, color = ? WHERE id = ?`,
		name, cleanColor(r.FormValue("color"), oldColor), id); err != nil {
		a.fail(w, r, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/stories/%d", storyID))
}

func (a *App) deleteCharacter(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var storyID int64
	if err := a.db.QueryRow(`SELECT story_id FROM characters WHERE id = ?`, id).Scan(&storyID); err != nil {
		a.fail(w, r, err)
		return
	}
	// events keep existing; ON DELETE SET NULL moves them to "Unassigned"
	if _, err := a.db.Exec(`DELETE FROM characters WHERE id = ?`, id); err != nil {
		a.fail(w, r, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/stories/%d", storyID))
}

// ---------- event handlers ----------

type EventForm struct {
	Story                          Story
	Characters                     []Characters
	Event                          Event
	Start, StartTime, End, EndTime string
	Error                          string
	IsNew                          bool
	Action                         string
}

func splitTime(s string) (string, string) {
	d, t, _ := strings.Cut(s, " ")
	return d, t
}

// readEvent parses and validates the submitted form.
func (a *App) readEvent(r *http.Request, st Story) (Event, EventForm, error) {
	f := EventForm{
		Story:     st,
		Start:     strings.TrimSpace(r.FormValue("start")),
		StartTime: strings.TrimSpace(r.FormValue("start_time")),
		End:       strings.TrimSpace(r.FormValue("end")),
		EndTime:   strings.TrimSpace(r.FormValue("end_time")),
	}
	ev := Event{
		StoryID:  st.ID,
		Title:    strings.TrimSpace(r.FormValue("title")),
		Notes:    strings.TrimSpace(r.FormValue("notes")),
		StartsAt: joinTime(st.Mode, f.Start, f.StartTime),
		EndsAt:   joinTime(st.Mode, f.End, f.EndTime),
	}
	if tid, err := strconv.ParseInt(r.FormValue("character"), 10, 64); err == nil && tid > 0 {
		var n int
		_ = a.db.QueryRow(`SELECT COUNT(*) FROM characters WHERE id = ? AND story_id = ?`, tid, st.ID).Scan(&n)
		if n == 1 {
			ev.CharacterID = sql.NullInt64{Int64: tid, Valid: true}
		}
	}
	f.Event = ev

	if ev.Title == "" {
		return ev, f, errors.New("Please give the event a title.")
	}
	startKey, err := parseKey(st.Mode, ev.StartsAt)
	if err != nil {
		return ev, f, errors.New("The start time isn't valid.")
	}
	if ev.EndsAt != "" {
		endKey, err := parseKey(st.Mode, ev.EndsAt)
		if err != nil {
			return ev, f, errors.New("The end time isn't valid.")
		}
		if endKey < startKey {
			return ev, f, errors.New("The end can't be before the start.")
		}
	}
	return ev, f, nil
}

func (a *App) newEventForm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	st, err := a.getStory(id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	characters, err := a.listCharacters(id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	f := EventForm{Story: st, Characters: characters, IsNew: true, Action: fmt.Sprintf("/stories/%d/events", id)}
	if tid, err := strconv.ParseInt(r.URL.Query().Get("character"), 10, 64); err == nil && tid > 0 {
		f.Event.CharacterID = sql.NullInt64{Int64: tid, Valid: true}
	}
	f.Start = r.URL.Query().Get("start")
	a.render(w, 200, "event_form", f)
}

func (a *App) createEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	st, err := a.getStory(id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	ev, f, verr := a.readEvent(r, st)
	if verr != nil {
		f.Characters, _ = a.listCharacters(id)
		f.Error, f.IsNew, f.Action = verr.Error(), true, fmt.Sprintf("/stories/%d/events", id)
		a.render(w, http.StatusBadRequest, "event_form", f)
		return
	}
	res, err := a.db.Exec(`INSERT INTO events (story_id, character_id, title, notes, starts_at, ends_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, ev.CharacterID, ev.Title, ev.Notes, ev.StartsAt, ev.EndsAt)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if r.FormValue("again") != "" {
		q := url.Values{}
		if ev.CharacterID.Valid {
			q.Set("character", strconv.FormatInt(ev.CharacterID.Int64, 10))
		}
		if f.Start != "" {
			q.Set("start", f.Start)
		}
		to := fmt.Sprintf("/stories/%d/events/new", id)
		if len(q) > 0 {
			to += "?" + q.Encode()
		}
		redirect(w, r, to)
		return
	}
	eid, _ := res.LastInsertId()
	redirect(w, r, fmt.Sprintf("/stories/%d#e%d", id, eid))
}

func (a *App) editEventForm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ev, err := a.getEvent(id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	st, err := a.getStory(ev.StoryID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	characters, err := a.listCharacters(st.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	f := EventForm{Story: st, Characters: characters, Event: ev, Action: fmt.Sprintf("/events/%d", id)}
	if st.IsDate() {
		f.Start, f.StartTime = splitTime(ev.StartsAt)
		f.End, f.EndTime = splitTime(ev.EndsAt)
	} else {
		f.Start, f.End = ev.StartsAt, ev.EndsAt
	}
	a.render(w, 200, "event_form", f)
}

func (a *App) updateEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	old, err := a.getEvent(id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	st, err := a.getStory(old.StoryID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	ev, f, verr := a.readEvent(r, st)
	if verr != nil {
		f.Event.ID = id
		f.Characters, _ = a.listCharacters(st.ID)
		f.Error, f.Action = verr.Error(), fmt.Sprintf("/events/%d", id)
		a.render(w, http.StatusBadRequest, "event_form", f)
		return
	}
	if _, err := a.db.Exec(`UPDATE events SET character_id = ?, title = ?, notes = ?, starts_at = ?, ends_at = ? WHERE id = ?`,
		ev.CharacterID, ev.Title, ev.Notes, ev.StartsAt, ev.EndsAt, id); err != nil {
		a.fail(w, r, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/stories/%d#e%d", st.ID, id))
}

func (a *App) deleteEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ev, err := a.getEvent(id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if _, err := a.db.Exec(`DELETE FROM events WHERE id = ?`, id); err != nil {
		a.fail(w, r, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/stories/%d", ev.StoryID))
}

// ---------- main ----------

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	dbPath := flag.String("db", "timeline.db", "path to the SQLite database file")
	flag.Parse()

	db, err := openDB(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()
	loadTemplates()
	app := &App{db: db}

	static, err := fs.Sub(assets, "static")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /{$}", app.index)
	mux.HandleFunc("POST /stories", app.createStory)
	mux.HandleFunc("GET /stories/{id}", app.storyPage)
	mux.HandleFunc("POST /stories/{id}/edit", app.updateStory)
	mux.HandleFunc("POST /stories/{id}/delete", app.deleteStory)
	mux.HandleFunc("POST /stories/{id}/characters", app.createCharacter)
	mux.HandleFunc("POST /characters/{id}", app.updateCharacter)
	mux.HandleFunc("POST /characters/{id}/delete", app.deleteCharacter)
	mux.HandleFunc("GET /stories/{id}/events/new", app.newEventForm)
	mux.HandleFunc("POST /stories/{id}/events", app.createEvent)
	mux.HandleFunc("GET /events/{id}/edit", app.editEventForm)
	mux.HandleFunc("POST /events/{id}", app.updateEvent)
	mux.HandleFunc("POST /events/{id}/delete", app.deleteEvent)

	log.Printf("Story Timeline running at http://%s  (database: %s)", *addr, *dbPath)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
