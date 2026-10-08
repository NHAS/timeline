// Story Timeline: a small web app for planning when things happen in your stories.
// Data lives in a single SQLite file; the UI is server-rendered HTML.
package main

import (
	"database/sql"
	"embed"
	"flag"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"regexp"

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
	ends_at   TEXT NOT NULL DEFAULT '{}'
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

func (a *App) index(w http.ResponseWriter, r *http.Request) {
	stories, err := a.listStories()
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.render(w, http.StatusOK, "index", map[string]any{"Stories": stories})
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
