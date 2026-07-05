package library

import (
	"os"
	"path/filepath"
	"testing"

	"gofin/internal/config"
	"gofin/internal/store"
)

func TestProviderIDsAndCleanName(t *testing.T) {
	name := "Dune 2021 [tmdbid-438631] [imdbid-tt1160419] [tvdbid-12345]"
	ids := providerIDs(name)
	if ids["tmdbid"] != "438631" || ids["imdbid"] != "tt1160419" || ids["tvdbid"] != "12345" {
		t.Fatalf("ids = %#v", ids)
	}
	if got := cleanProviders(name); got != "Dune 2021" {
		t.Fatalf("clean = %q", got)
	}
}

func TestSmartScanSkipsUnchangedAndDeletesMissing(t *testing.T) {
	dir := t.TempDir()
	movies := filepath.Join(dir, "Movies")
	if err := os.MkdirAll(movies, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(movies, "Example Movie 2024.mp4")
	if err := os.WriteFile(file, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	libs := []config.Library{{Name: "Movies", Type: "movies", Path: movies}}
	if err := (Scanner{Store: s, scanID: "scan1"}).Scan(libs, ""); err != nil {
		t.Fatal(err)
	}
	id := store.StableID("item", file)
	if err := s.UpsertItem(store.Item{ID: id, LibraryID: store.StableID("library", movies), ParentID: store.StableID("library", movies), Type: "Movie", Name: "Changed Name", LastSeenScan: "scan1"}); err != nil {
		t.Fatal(err)
	}
	if err := (Scanner{Store: s, scanID: "scan2"}).Scan(libs, ""); err != nil {
		t.Fatal(err)
	}
	it, err := s.Item(id)
	if err != nil {
		t.Fatal(err)
	}
	if it.Name != "Changed Name" || it.LastSeenScan != "scan2" {
		t.Fatalf("unchanged scan rewrote item: %#v", it)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := (Scanner{Store: s, scanID: "scan3"}).Scan(libs, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Item(id); err == nil {
		t.Fatal("deleted file item still exists")
	}
}
