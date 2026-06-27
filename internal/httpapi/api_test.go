package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gofin/internal/config"
	"gofin/internal/library"
	"gofin/internal/store"
)

func TestRichMovieDTO(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	must(t, err)
	defer s.Close()
	c := config.Config{Server: config.Server{Name: "GoFin Test", ID: "server-1"}, Libraries: []config.Library{{Name: "Movies", Type: "movies", Path: dir}}}
	must(t, s.SaveLibraries([]store.Library{{ID: "lib", Name: "Movies", Type: "movies", Path: dir}}))
	must(t, s.UpsertItem(store.Item{ID: "m1", LibraryID: "lib", ParentID: "lib", Type: "Movie", Name: "Dune", Path: filepath.Join(dir, "Dune.mkv"), Container: "mkv", GenresJSON: `["Science Fiction"]`, StudiosJSON: `["Legendary Pictures"]`, PeopleJSON: `[{"Name":"Denis Villeneuve","Role":"Director","Type":"Director"}]`, TaglinesJSON: `["It begins"]`, ExternalURLsJSON: `[{"Name":"IMDb","URL":"https://www.imdb.com/title/tt1160419/"}]`, CommunityRating: 8.1, OfficialRating: "PG-13", RuntimeTicks: 93000000000, ProviderIDsJSON: `{"Tmdb":"438631"}`}))
	h := API{C: c, S: s}.Handler()
	page := get(t, h, "/Items?IncludeItemTypes=Movie", nil, http.StatusOK)
	movie := page["Items"].([]any)[0].(map[string]any)
	if len(movie["Genres"].([]any)) != 1 || len(movie["People"].([]any)) != 1 || movie["RunTimeTicks"].(float64) == 0 {
		t.Fatalf("missing rich metadata: %#v", movie)
	}
	ms := movie["MediaSources"].([]any)[0].(map[string]any)
	if ms["RunTimeTicks"].(float64) == 0 || movie["MediaSourceCount"].(float64) != 1 {
		t.Fatalf("missing media source metadata: %#v", movie)
	}
}

func TestFiltersSortAndUserData(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	must(t, err)
	defer s.Close()
	must(t, s.SaveLibraries([]store.Library{{ID: "lib", Name: "Movies", Type: "movies", Path: dir}}))
	must(t, s.AddUser("admin", "pass"))
	must(t, s.UpsertItem(store.Item{ID: "m1", LibraryID: "lib", ParentID: "lib", Type: "Movie", Name: "Low", GenresJSON: `["Drama"]`, OfficialRating: "PG", CommunityRating: 2}))
	must(t, s.UpsertItem(store.Item{ID: "m2", LibraryID: "lib", ParentID: "lib", Type: "Movie", Name: "High", GenresJSON: `["Action"]`, OfficialRating: "R", CommunityRating: 9, RuntimeTicks: 100}))
	h := API{C: config.Config{Server: config.Server{ID: "server-1"}}, S: s}.Handler()
	auth := post(t, h, "/Users/AuthenticateByName", nil, []byte(`{"Username":"admin","Pw":"pass"}`), http.StatusOK)
	head := map[string]string{"X-Emby-Token": auth["AccessToken"].(string)}
	page := get(t, h, "/Items?IncludeItemTypes=Movie&SortBy=CommunityRating&SortOrder=Descending", head, http.StatusOK)
	if firstID(page) != "m2" {
		t.Fatalf("sort did not put high rating first: %#v", page)
	}
	filters := get(t, h, "/Items/Filters", head, http.StatusOK)
	if len(filters["Genres"].([]any)) != 2 || len(filters["OfficialRatings"].([]any)) != 2 {
		t.Fatalf("bad filters: %#v", filters)
	}
	post(t, h, "/Sessions/Playing/Progress", head, []byte(`{"ItemId":"m2","PositionTicks":90}`), http.StatusNoContent)
	item := get(t, h, "/Items/m2", head, http.StatusOK)
	if item["UserData"].(map[string]any)["PlaybackPositionTicks"].(float64) != 90 {
		t.Fatalf("bad user data: %#v", item["UserData"])
	}
}

func TestPlezyBaseFlow(t *testing.T) {
	dir := t.TempDir()
	movies := filepath.Join(dir, "Movies")
	tv := filepath.Join(dir, "TV")
	must(t, os.MkdirAll(movies, 0755))
	must(t, os.MkdirAll(filepath.Join(tv, "Example Show", "Season 1"), 0755))
	must(t, os.WriteFile(filepath.Join(movies, "Test Movie 2024.mp4"), []byte("fake-video"), 0644))
	must(t, os.WriteFile(filepath.Join(tv, "Example Show", "Season 1", "Example.Show.S01E01.mp4"), []byte("fake-episode"), 0644))

	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	must(t, err)
	defer s.Close()
	c := config.Config{Server: config.Server{Name: "GoFin Test", ID: "server-1"}, Libraries: []config.Library{{Name: "Movies", Type: "movies", Path: movies}, {Name: "TV Shows", Type: "tvshows", Path: tv}}}
	must(t, s.AddUser("admin", "pass"))
	must(t, (library.Scanner{Store: s}).Scan(c.Libraries, ""))
	h := API{C: c, S: s}.Handler()

	get(t, h, "/System/Info/Public", nil, http.StatusOK)
	get(t, h, "/Users/Public", nil, http.StatusOK)

	auth := post(t, h, "/Users/AuthenticateByName", nil, []byte(`{"Username":"admin","Pw":"pass"}`), http.StatusOK)
	token := auth["AccessToken"].(string)
	head := map[string]string{"X-Emby-Token": token}

	views := get(t, h, "/Users/user/Views", head, http.StatusOK)
	if count(views) != 2 {
		t.Fatalf("views count = %d", count(views))
	}

	moviesPage := get(t, h, "/Items?IncludeItemTypes=Movie", head, http.StatusOK)
	if count(moviesPage) != 1 {
		t.Fatalf("movie count = %d", count(moviesPage))
	}
	movieID := firstID(moviesPage)

	pb := post(t, h, "/Items/"+movieID+"/PlaybackInfo", head, []byte(`{}`), http.StatusOK)
	if len(pb["MediaSources"].([]any)) != 1 {
		t.Fatal("expected one media source")
	}

	stream := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/Videos/"+movieID+"/stream?Static=true&api_key="+token, nil)
	h.ServeHTTP(stream, req)
	if stream.Code != http.StatusOK || stream.Body.String() != "fake-video" {
		t.Fatalf("bad stream: %d %q", stream.Code, stream.Body.String())
	}

	seriesPage := get(t, h, "/Items?IncludeItemTypes=Series", head, http.StatusOK)
	seriesID := firstID(seriesPage)
	seasons := get(t, h, "/Shows/"+seriesID+"/Seasons", head, http.StatusOK)
	if count(seasons) != 1 {
		t.Fatalf("season count = %d", count(seasons))
	}
	episodes := get(t, h, "/Shows/"+seriesID+"/Episodes", head, http.StatusOK)
	if count(episodes) != 1 {
		t.Fatalf("episode count = %d", count(episodes))
	}

	post(t, h, "/Sessions/Playing/Progress", head, []byte(`{"ItemId":"`+movieID+`","PositionTicks":50000000}`), http.StatusNoContent)
	resume := get(t, h, "/UserItems/Resume", head, http.StatusOK)
	if count(resume) != 1 {
		t.Fatalf("resume count = %d", count(resume))
	}

	movieLibID := store.StableID("library", movies)
	tvLibID := store.StableID("library", tv)
	latestMovies := get(t, h, "/Items/Latest?ParentId="+movieLibID+"&IncludeItemTypes=Movie", head, http.StatusOK)
	if count(latestMovies) != 1 {
		t.Fatalf("latest movies count = %d", count(latestMovies))
	}
	latestTVMovies := get(t, h, "/Items/Latest?ParentId="+tvLibID+"&IncludeItemTypes=Movie", head, http.StatusOK)
	if count(latestTVMovies) != 0 {
		t.Fatalf("latest tv movies count = %d", count(latestTVMovies))
	}
	get(t, h, "/Users/user/Items/Latest", head, http.StatusOK)
	get(t, h, "/Items/Counts", head, http.StatusOK)
	get(t, h, "/Items/Filters", head, http.StatusOK)
	get(t, h, "/Shows/NextUp", head, http.StatusOK)
}

func get(t *testing.T, h http.Handler, path string, headers map[string]string, want int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return do(t, h, req, want)
}

func post(t *testing.T, h http.Handler, path string, headers map[string]string, body []byte, want int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return do(t, h, req, want)
}

func do(t *testing.T, h http.Handler, req *http.Request, want int) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("%s %s = %d, want %d: %s", req.Method, req.URL.Path, w.Code, want, w.Body.String())
	}
	if w.Body.Len() == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if json.Unmarshal(w.Body.Bytes(), &out) == nil {
		return out
	}
	var arr []any
	must(t, json.Unmarshal(w.Body.Bytes(), &arr))
	return map[string]any{"Items": arr}
}

func count(m map[string]any) int      { return len(m["Items"].([]any)) }
func firstID(m map[string]any) string { return m["Items"].([]any)[0].(map[string]any)["Id"].(string) }
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
