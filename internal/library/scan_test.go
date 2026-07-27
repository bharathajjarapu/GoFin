package library

import (
	"bytes"
	"encoding/binary"

	"context"
	"gofin/internal/audio"
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
	if err := (Scanner{Store: s}).ScanContext(context.Background(), []config.Library{{Name: "TV", Type: "tvshows", Path: tv}}, ""); err != nil {
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
	if err := (Scanner{Store: s, scanID: "scan1"}).ScanContext(context.Background(), libs, ""); err != nil {
		t.Fatal(err)
	}
	id := store.StableID("item", file)
	if err := s.UpsertItem(store.Item{ID: id, LibraryID: store.StableID("library", movies), ParentID: store.StableID("library", movies), Type: "Movie", Name: "Changed Name", LastSeenScan: "scan1"}); err != nil {
		t.Fatal(err)
	}
	if err := (Scanner{Store: s, scanID: "scan2"}).ScanContext(context.Background(), libs, ""); err != nil {
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
	if err := (Scanner{Store: s, scanID: "scan3"}).ScanContext(context.Background(), libs, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Item(id); err == nil {
		t.Fatal("deleted file item still exists")
	}
}

func TestUnavailableLibraryIsPreserved(t *testing.T) {
	dir := t.TempDir()
	movies := filepath.Join(dir, "Movies")
	if err := os.MkdirAll(movies, 0755); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	libs := []config.Library{{Name: "Movies", Type: "movies", Path: movies}}
	if err := (Scanner{Store: s}).ScanContext(context.Background(), libs, ""); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(movies); err != nil {
		t.Fatal(err)
	}
	if err := (Scanner{Store: s}).ScanContext(context.Background(), libs, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.Libraries()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != movies {
		t.Fatalf("libraries = %#v", got)
	}
}

func TestScanContextCancellation(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "gofin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Scanner{Store: s}).ScanContext(ctx, nil, ""); err != context.Canceled {
		t.Fatalf("error = %v", err)
	}
}

// flacTrack writes a minimal FLAC file whose STREAMINFO reports one second and
// whose comment block carries the given tags.
func flacTrack(t *testing.T, path string, entries ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	b.WriteString("fLaC")
	info := make([]byte, 34)
	binary.BigEndian.PutUint64(info[10:18], 44100<<44|44100)
	b.WriteByte(0)
	b.Write([]byte{0, 0, byte(len(info))})
	b.Write(info)

	var c bytes.Buffer
	binary.Write(&c, binary.LittleEndian, uint32(0))
	binary.Write(&c, binary.LittleEndian, uint32(len(entries)))
	for _, e := range entries {
		binary.Write(&c, binary.LittleEndian, uint32(len(e)))
		c.WriteString(e)
	}
	b.WriteByte(4 | 0x80)
	b.Write([]byte{byte(c.Len() >> 16), byte(c.Len() >> 8), byte(c.Len())})
	b.Write(c.Bytes())
	if err := os.WriteFile(path, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestScanMusicBuildsArtistAlbumTrack(t *testing.T) {
	dir := t.TempDir()
	music := filepath.Join(dir, "Music")
	// Folder names deliberately disagree with the tags: tags must win, which is
	// what keeps compilations and re-tagged files in the right album.
	flacTrack(t, filepath.Join(music, "Wrong Folder", "Wrong Album", "01.flac"),
		"TITLE=Blue Monday", "ALBUM=Power", "ALBUMARTIST=New Order", "ARTIST=New Order",
		"TRACKNUMBER=1", "DISCNUMBER=1", "DATE=1983", "GENRE=Post-Punk")
	flacTrack(t, filepath.Join(music, "Wrong Folder", "Wrong Album", "02.flac"),
		"TITLE=The Beach", "ALBUM=Power", "ALBUMARTIST=New Order", "ARTIST=New Order", "TRACKNUMBER=2")

	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	libs := []config.Library{{Name: "Music", Type: "music", Path: music}}
	if err := (Scanner{Store: s}).ScanContext(context.Background(), libs, ""); err != nil {
		t.Fatal(err)
	}

	artists, err := s.Items(store.ItemQuery{Type: "MusicArtist"})
	if err != nil {
		t.Fatal(err)
	}
	if len(artists) != 1 || artists[0].Name != "New Order" || !artists[0].IsFolder {
		t.Fatalf("artists = %#v", artists)
	}
	albums, err := s.Items(store.ItemQuery{Type: "MusicAlbum", ParentID: artists[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 || albums[0].Name != "Power" || albums[0].ProductionYear != 1983 {
		t.Fatalf("albums = %#v", albums)
	}
	tracks, err := s.Items(store.ItemQuery{Type: "Audio", ParentID: albums[0].ID, SortBy: "ParentIndexNumber"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 || tracks[0].Name != "Blue Monday" || tracks[1].Name != "The Beach" {
		t.Fatalf("tracks = %#v", tracks)
	}
	first := tracks[0]
	if first.Album != "Power" || first.AlbumArtist != "New Order" || first.IndexNumber != 1 ||
		first.RuntimeTicks != audio.TicksPerSecond || first.GenresJSON != `["Post-Punk"]` {
		t.Fatalf("track = %#v", first)
	}
}

// An untagged file still has to land somewhere, so the folder layout fills in.
func TestScanMusicFallsBackToFolders(t *testing.T) {
	dir := t.TempDir()
	music := filepath.Join(dir, "Music")
	flacTrack(t, filepath.Join(music, "Some Artist", "Some Album", "Some Song.flac"))
	flacTrack(t, filepath.Join(music, "loose.flac"))

	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	libs := []config.Library{{Name: "Music", Type: "music", Path: music}}
	if err := (Scanner{Store: s}).ScanContext(context.Background(), libs, ""); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	artists, err := s.Items(store.ItemQuery{Type: "MusicArtist"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range artists {
		names[a.Name] = true
	}
	if !names["Some Artist"] || !names["Unknown Artist"] {
		t.Fatalf("artists = %#v", artists)
	}
	tracks, err := s.Items(store.ItemQuery{Type: "Audio"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 {
		t.Fatalf("tracks = %#v", tracks)
	}
}

// A rescan must not orphan the artist and album of an unchanged track.
func TestScanMusicKeepsParentsOnRescan(t *testing.T) {
	dir := t.TempDir()
	music := filepath.Join(dir, "Music")
	flacTrack(t, filepath.Join(music, "Artist", "Album", "01.flac"), "TITLE=Song", "ALBUM=Album", "ALBUMARTIST=Artist")
	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	libs := []config.Library{{Name: "Music", Type: "music", Path: music}}
	if err := (Scanner{Store: s, scanID: "scan1"}).ScanContext(context.Background(), libs, ""); err != nil {
		t.Fatal(err)
	}
	if err := (Scanner{Store: s, scanID: "scan2"}).ScanContext(context.Background(), libs, ""); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"MusicArtist", "MusicAlbum", "Audio"} {
		items, err := s.Items(store.ItemQuery{Type: typ})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("%s survived rescan as %#v", typ, items)
		}
	}
}

// Video containers must not be indexed as tracks, and audio must not be
// indexed as video, so the two library types never claim each other's files.
func TestScanIgnoresForeignContainers(t *testing.T) {
	dir := t.TempDir()
	music := filepath.Join(dir, "Music")
	if err := os.MkdirAll(music, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(music, "clip.mp4"), []byte("not audio"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := (Scanner{Store: s}).ScanContext(context.Background(), []config.Library{{Name: "Music", Type: "music", Path: music}}, ""); err != nil {
		t.Fatal(err)
	}
	items, err := s.Items(store.ItemQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Type != "CollectionFolder" {
			t.Fatalf("music scan indexed %#v", it)
		}
	}
}
