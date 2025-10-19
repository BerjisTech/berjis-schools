package migrate

import (
    "fmt"
    "io/fs"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "time"

    "github.com/jmoiron/sqlx"
)

type Runner struct { Dir string }

func (r Runner) filenames() ([]string, error) {
    files := []string{}
    err := filepath.WalkDir(r.Dir, func(path string, d fs.DirEntry, err error) error {
        if err != nil { return err }
        if d.IsDir() { return nil }
        if strings.HasSuffix(d.Name(), ".sql") { files = append(files, path) }
        return nil
    })
    if err != nil { return nil, err }
    sort.Strings(files)
    return files, nil
}

func ensureTable(db *sqlx.DB) error {
    _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
        id TEXT PRIMARY KEY,
        applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
    )`)
    return err
}

func applied(db *sqlx.DB, id string) (bool, error) {
    var exists bool
    if err := db.Get(&exists, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE id=$1)`, id); err != nil {
        return false, err
    }
    return exists, nil
}

func apply(db *sqlx.DB, id, sql string) error {
    tx, err := db.Beginx()
    if err != nil { return err }
    defer tx.Rollback()
    if _, err := tx.Exec(sql); err != nil { return fmt.Errorf("migration %s failed: %w", id, err) }
    if _, err := tx.Exec(`INSERT INTO schema_migrations (id, applied_at) VALUES ($1, $2)`, id, time.Now().UTC()); err != nil { return err }
    return tx.Commit()
}

func (r Runner) Up(db *sqlx.DB) error {
    if err := ensureTable(db); err != nil { return err }
    files, err := r.filenames()
    if err != nil { return err }
    for _, f := range files {
        id := filepath.Base(f)
        done, err := applied(db, id)
        if err != nil { return err }
        if done { continue }
        b, err := os.ReadFile(f)
        if err != nil { return err }
        if err := apply(db, id, string(b)); err != nil { return err }
    }
    return nil
}