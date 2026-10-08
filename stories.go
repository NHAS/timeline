package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type Story struct {
	ID          int64
	Title       string
	Description string
	Mode        string
	Unit        string
	EventCount  int
}

func (s Story) IsDate() bool { return s.Mode != "number" }

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

	CharactersOpen bool   // render the Tracks panel expanded
	Notice         string // error shown at the top of the Tracks panel

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

	p.CharactersOpen = r.URL.Query().Get("panel") == "tracks"
	if r.URL.Query().Get("err") == "date" {
		p.Notice = "That date isn't valid, so the track wasn't created."
		p.CharactersOpen = true
	}

	a.render(w, http.StatusOK, "story", p)
}
