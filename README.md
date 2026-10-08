# Story Timeline

A small Go web app for planning when things happen in your stories.
Data is stored in a single SQLite file; the interface runs in your browser.

## Run it

Requires Go 1.22 or newer (uses the built-in router).

```bash
go mod tidy      # downloads the pure-Go SQLite driver (no C compiler needed)
go run .
```

Then open http://127.0.0.1:8080

Options:

```bash
go run . -addr 127.0.0.1:9000 -db my-stories.db
```

The database file (`timeline.db` by default) is created on first run. Back it up by copying it.
The app has no login, so it listens on localhost only by default. Don't expose it to a network unless you add authentication.

## How it works

- **Stories**: each story has its own timeline. When creating one, choose how time works:
  - *Calendar dates* (real or invented, with optional times), or
  - *Numbers* with a unit label (Day 3, Year 212, Chapter 7). Handy for fictional calendars.
- **Events**: a title, notes, a start, an optional end, and an optional character.
- **characters**: colour-coded event groups, e.g. one per character, place or plot thread.
- **Views**: a vertical *Timeline*, or *Swimlanes* with one column per character. Filter either by character.
- "Save & add another" on the event form keeps the character and date filled in, for quickly entering a run of events.

## Files

```bash
main.go              server, database access, handlers
templates/*.html     page templates (embedded in the binary)
static/              CSS and a little JavaScript (embedded in the binary)
```

`go build` produces one self-contained binary; the templates and static files are embedded.
