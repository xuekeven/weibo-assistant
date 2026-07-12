package migrate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMigrationFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "002_second.sql"), []byte("select 2;"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "001_first.sql"), []byte("select 1;"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := loadMigrationFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Version != 1 || files[1].Version != 2 {
		t.Fatalf("unexpected migration order: %#v", files)
	}
	if files[0].Checksum == "" || files[0].Name != "first" {
		t.Fatalf("unexpected migration metadata: %#v", files[0])
	}
}

func TestLoadMigrationFilesRejectsDuplicateVersion(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"001_first.sql", "001_again.sql"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("select 1;"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := loadMigrationFiles(dir); err == nil {
		t.Fatal("expected duplicate migration version error")
	}
}
