package store

import (
	"path/filepath"
	"strconv"
	"testing"
)

func TestSavePeople(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "gofin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveLibraries([]Library{{ID: "lib", Name: "Movies", Type: "movies", Path: t.TempDir()}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertItem(Item{ID: "m1", LibraryID: "lib", ParentID: "lib", Type: "Movie", Name: "Dune"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePeople("m1", []Person{{TMDBID: 1190668, Name: "Timothee Chalamet", Role: "Actor", Character: "Paul", ProfileURL: "https://image.tmdb.org/t/p/original/a.jpg"}}); err != nil {
		t.Fatal(err)
	}
	img, err := s.PersonImage("timothee chalamet")
	if err != nil {
		t.Fatal(err)
	}
	if img != "https://image.tmdb.org/t/p/original/a.jpg" {
		t.Fatalf("image = %q", img)
	}
	var links int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM item_people WHERE item_id='m1'`).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 1 {
		t.Fatalf("links = %d", links)
	}
	people, err := s.People(PersonQuery{Search: "timothee", Types: "Actor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 1 || people[0].TMDBID != 1190668 {
		t.Fatalf("people = %#v", people)
	}
}

func TestForeignKeysCascade(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "gofin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveLibraries([]Library{{ID: "lib", Name: "Movies", Type: "movies", Path: t.TempDir()}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertItem(Item{ID: "m1", LibraryID: "lib", ParentID: "lib", Type: "Movie", Name: "Dune", Path: filepath.Join(t.TempDir(), "dune.mkv")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DELETE FROM items WHERE id='m1'`); err != nil {
		t.Fatal(err)
	}
	var sources int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM media_sources WHERE item_id='m1'`).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if sources != 0 {
		t.Fatalf("media sources = %d", sources)
	}
}

func TestBatchImagesAndPlayback(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "gofin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveLibraries([]Library{{ID: "lib", Name: "Movies", Type: "movies", Path: t.TempDir()}}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUserPolicy("u", "pass", true, false, 0); err != nil {
		t.Fatal(err)
	}
	ids := []string{"m1", "m2"}
	for i := 0; i < 905; i++ {
		ids = append(ids, "x"+strconv.Itoa(i))
	}
	for _, id := range ids {
		if err := s.UpsertItem(Item{ID: id, LibraryID: "lib", ParentID: "lib", Type: "Movie", Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertImage(Image{ItemID: "m1", Type: "Primary", Path: "poster.jpg", Tag: "poster"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFavorite(StableID("user", "u"), "m2", true); err != nil {
		t.Fatal(err)
	}
	imgs, err := s.ImagesByItemIDs(ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs["m1"]) != 1 || imgs["m1"][0].Tag != "poster" {
		t.Fatalf("images = %#v", imgs)
	}
	playback, err := s.PlaybackByItemIDs(StableID("user", "u"), ids)
	if err != nil {
		t.Fatal(err)
	}
	if playback["m2"].Favorite != true || playback["m1"].Favorite {
		t.Fatalf("playback = %#v", playback)
	}
}

func BenchmarkItemsBrowse(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "gofin.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveLibraries([]Library{{ID: "lib", Name: "Movies", Type: "movies", Path: b.TempDir()}}); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		if err := s.UpsertItem(Item{ID: "m" + strconv.Itoa(i), LibraryID: "lib", ParentID: "lib", Type: "Movie", Name: "Movie " + strconv.Itoa(i), ProductionYear: 2000 + i%20}); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Items(ItemQuery{ParentID: "lib", Type: "Movie", SortBy: "SortName", Limit: 100}); err != nil {
			b.Fatal(err)
		}
	}
}
