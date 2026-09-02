package main

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestLoadMigrationsSortsAndChecksVersions(t *testing.T) {
	filesystem := fstest.MapFS{
		"0002_second.sql": &fstest.MapFile{Data: []byte("SELECT 2")},
		"README.md":       &fstest.MapFile{Data: []byte("ignored")},
		"0001_first.sql":  &fstest.MapFile{Data: []byte("SELECT 1")},
	}
	items, err := loadMigrations(filesystem)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].version != "0001" || items[1].version != "0002" {
		t.Fatalf("unexpected order: %+v", items)
	}

	filesystem["0001_duplicate.sql"] = &fstest.MapFile{Data: []byte("SELECT 3")}
	if _, err = loadMigrations(filesystem); err == nil {
		t.Fatal("expected duplicate migration version error")
	}
}

func TestLoadMigrationsRejectsEmptySet(t *testing.T) {
	_, err := loadMigrations(fstest.MapFS{"README.md": &fstest.MapFile{Data: []byte("none")}})
	if err == nil {
		t.Fatal("expected missing migration error")
	}
	if _, ok := err.(*fs.PathError); ok {
		t.Fatalf("expected safe domain error, got %T", err)
	}
}
