package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

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
