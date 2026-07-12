package main

import (
    "database/sql"
    "fmt"
    "io/fs"
    "os"
    "path/filepath"
    "sort"
)

func RunMigrations(db *sql.DB, dir string) error {
    entries, err := os.ReadDir(dir)
    if err != nil {
        return fmt.Errorf("read migrations dir: %w", err)
    }

    var files []fs.DirEntry
    for _, e := range entries {
        if filepath.Ext(e.Name()) == ".sql" {
            files = append(files, e)
        }
    }

    sort.Slice(files, func(i, j int) bool {
        return files[i].Name() < files[j].Name()
    })

    for _, f := range files {
        path := filepath.Join(dir, f.Name())
        b, err := os.ReadFile(path)
        if err != nil {
            return err
        }

        if _, err := db.Exec(string(b)); err != nil {
            return fmt.Errorf("run migration %s: %w", f.Name(), err)
        }
    }

    return nil
}
