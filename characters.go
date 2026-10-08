package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type Characters struct {
	ID      int64
	StoryID int64
	Name    string
	Color   string
	Count   int
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
	if name == "" {
		redirect(w, r, charactersURL(id, ""))
		return
	}

	// Optional first event: validate before touching the database.
	eventAt := strings.TrimSpace(r.FormValue("start"))
	if _, err := parseTime(eventAt); err != nil {

		redirect(w, r, charactersURL(id, "date"))
		return
	}

	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM characters WHERE story_id = ?`, id).Scan(&n)
	color := cleanColor(r.FormValue("color"), palette[n%len(palette)])

	tx, err := a.db.Begin()
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer tx.Rollback() // no-op once committed
	res, err := tx.Exec(`INSERT INTO characters (story_id, name, color) VALUES (?, ?, ?)`, id, name, color)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if eventAt != "" {
		characterID, _ := res.LastInsertId()
		title := strings.TrimSpace(r.FormValue("event_title"))
		if title == "" {
			title = name + " born"
		}
		if _, err := tx.Exec(`INSERT INTO events (story_id, character_id, title, notes, starts_at, ends_at) VALUES (?, ?, ?, '', ?, '')`,
			id, characterID, title, eventAt); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		a.fail(w, r, err)
		return
	}
	redirect(w, r, charactersURL(id, ""))

}

func charactersURL(storyID int64, errCode string) string {
	u := fmt.Sprintf("/stories/%d?panel=characters", storyID)
	if errCode != "" {
		u += "&err=" + url.QueryEscape(errCode)
	}
	return u + "#characters"
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
