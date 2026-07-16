package library

import (
	"os"
	"path/filepath"
	"testing"

	"gofin/internal/config"
	"gofin/internal/metadata"
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

func TestFlatTVEpisodeUsesSeriesNameWithoutEpisodeToken(t *testing.T) {
	dir := t.TempDir()
	tv := filepath.Join(dir, "TV")
	if err := os.MkdirAll(tv, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(tv, "Avatar The Last Airbender S02E01.mkv")
	if err := os.WriteFile(file, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := (Scanner{Store: s}).Scan([]config.Library{{Name: "TV", Type: "tvshows", Path: tv}}, ""); err != nil {
		t.Fatal(err)
	}
	libID := store.StableID("library", tv)
	seriesID := store.StableID("series", libID, "Avatar The Last Airbender")
	series, err := s.Item(seriesID)
	if err != nil {
		t.Fatal(err)
	}
	if series.Name != "Avatar The Last Airbender" {
		t.Fatalf("series name = %q", series.Name)
	}
	items, err := s.Items(store.ItemQuery{Type: "Episode"})
	if err != nil || len(items) != 1 || items[0].ParentIndexNumber != 2 || items[0].IndexNumber != 1 {
		t.Fatalf("episode parse failed: items=%#v err=%v", items, err)
	}
}

func TestFlatTVEpisodePreservesProviderIDs(t *testing.T) {
	series, ids := episodeSeriesSource([]string{"Show S01E01 [tmdbid-12345].mkv"}, "Show S01E01 [tmdbid-12345].mkv")
	if series != "Show" || ids["tmdbid"] != "12345" {
		t.Fatalf("flat source = %q, %#v", series, ids)
	}
}

func TestMetadataNoMatchMarkerSkipsRetry(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lib := store.Library{ID: "library", Name: "Movies", Type: "movies", Path: dir}
	if err := s.SaveLibraries([]store.Library{lib}); err != nil {
		t.Fatal(err)
	}
	scanner := Scanner{Store: s, Meta: metadata.Client{Enabled: true, Key: "key"}}
	item := store.Item{ID: "item", LibraryID: lib.ID, ParentID: lib.ID, Type: "Movie", Name: "Unmatched"}
	scanner.markMetadataMiss(&item)
	if item.ProviderIDsJSON != metadataNoMatch {
		t.Fatalf("marker = %q", item.ProviderIDsJSON)
	}
	if err := s.UpsertItem(item); err != nil {
		t.Fatal(err)
	}
	if scanner.needsMetadata(item.ID) {
		t.Fatal("no-match marker should not retry")
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
