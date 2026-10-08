package main

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

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

func cleanColor(c string, fallback string) string {
	c = strings.TrimSpace(c)
	if colorRe.MatchString(c) {
		return strings.ToLower(c)
	}
	return fallback
}
