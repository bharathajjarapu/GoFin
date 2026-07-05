package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestPeopleDTOAndImageRedirect(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	must(t, err)
	defer s.Close()
	must(t, s.SaveLibraries([]store.Library{{ID: "lib", Name: "Movies", Type: "movies", Path: dir}}))
	must(t, s.UpsertItem(store.Item{ID: "m1", LibraryID: "lib", ParentID: "lib", Type: "Movie", Name: "Dune", PeopleJSON: `[{"Name":"Timothee Chalamet","Role":"Paul Atreides","Type":"Actor","ProfileURL":"https://image.tmdb.org/t/p/original/a.jpg","TMDBID":1190668}]`}))
	must(t, s.SavePeople("m1", []store.Person{{TMDBID: 1190668, Name: "Timothee Chalamet", Role: "Actor", Character: "Paul Atreides", ProfileURL: "https://image.tmdb.org/t/p/original/a.jpg"}}))
	personID := store.StableID("person", "1190668")
	must(t, s.UpdatePersonDetails(store.Person{ID: personID, IMDBID: "nm3154303", Biography: "Actor bio", BirthDate: "1995-12-27", PlaceOfBirth: "New York", KnownDepartment: "Acting"}))

	h := API{C: config.Config{Server: config.Server{ID: "server-1"}}, S: s}.Handler()
	page := get(t, h, "/Items?IncludeItemTypes=Movie", nil, http.StatusOK)
	person := page["Items"].([]any)[0].(map[string]any)["People"].([]any)[0].(map[string]any)
	if person["PrimaryImageTag"] == nil || person["ProviderIds"].(map[string]any)["Tmdb"] != "1190668" {
		t.Fatalf("missing person metadata: %#v", person)
	}
	filtered := get(t, h, "/Items?PersonIds="+personID, nil, http.StatusOK)
	if count(filtered) != 1 || firstID(filtered) != "m1" {
		t.Fatalf("bad person filter: %#v", filtered)
	}
	persons := get(t, h, "/Persons?SearchTerm=timothee", nil, http.StatusOK)
	if count(persons) != 1 {
		t.Fatalf("person count = %d", count(persons))
	}
	detail := get(t, h, "/Persons/Timothee%20Chalamet", nil, http.StatusOK)
	if detail["Name"] != "Timothee Chalamet" || detail["Type"] != "Person" || detail["Overview"] != "Actor bio" || detail["RecursiveItemCount"].(float64) != 1 {
		t.Fatalf("bad person detail: %#v", detail)
	}
	hints := get(t, h, "/Search/Hints?SearchTerm=timothee", nil, http.StatusOK)
	if len(hints["SearchHints"].([]any)) != 1 || hints["SearchHints"].([]any)[0].(map[string]any)["Type"] != "Person" {
		t.Fatalf("bad search hints: %#v", hints)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/Persons/Timothee%20Chalamet/Images/Primary", nil)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "https://image.tmdb.org/t/p/original/a.jpg" {
		t.Fatalf("bad person image redirect: %d %q", w.Code, w.Header().Get("Location"))
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/Items/"+personID+"/Images/Primary?tag="+person["PrimaryImageTag"].(string), nil)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "https://image.tmdb.org/t/p/original/a.jpg" {
		t.Fatalf("bad Plezy person image redirect: %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestFiltersSortAndUserData(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	must(t, err)
	defer s.Close()
	must(t, s.SaveLibraries([]store.Library{{ID: "lib", Name: "Movies", Type: "movies", Path: dir}}))
	must(t, s.AddUserPolicy("admin", "pass", true, false, 0))
	must(t, s.UpsertItem(store.Item{ID: "m1", LibraryID: "lib", ParentID: "lib", Type: "Movie", Name: "Low", GenresJSON: `["Drama"]`, OfficialRating: "PG", CommunityRating: 2}))
	must(t, s.UpsertItem(store.Item{ID: "m2", LibraryID: "lib", ParentID: "lib", Type: "Movie", Name: "High", GenresJSON: `["Action"]`, OfficialRating: "R", ProductionYear: 2024, CommunityRating: 9, RuntimeTicks: 100}))
	h := API{C: config.Config{Server: config.Server{ID: "server-1"}}, S: s}.Handler()
	auth := post(t, h, "/Users/AuthenticateByName", nil, []byte(`{"Username":"admin","Pw":"pass"}`), http.StatusOK)
	head := map[string]string{"X-Emby-Token": auth["AccessToken"].(string)}
	page := get(t, h, "/Items?IncludeItemTypes=Movie&SortBy=CommunityRating&SortOrder=Descending", head, http.StatusOK)
	if firstID(page) != "m2" {
		t.Fatalf("sort did not put high rating first: %#v", page)
	}
	filters := get(t, h, "/Items/Filters", head, http.StatusOK)
	if len(filters["Genres"].([]any)) != 2 || len(filters["OfficialRatings"].([]any)) != 2 || len(filters["Years"].([]any)) != 1 {
		t.Fatalf("bad filters: %#v", filters)
	}
	byPremiere := get(t, h, "/Items?IncludeItemTypes=Movie&SortBy=PremiereDate,ProductionYear,SortName&SortOrder=Descending", head, http.StatusOK)
	if firstID(byPremiere) != "m2" {
		t.Fatalf("bad Plezy premiere sort: %#v", byPremiere)
	}
	filtered := get(t, h, "/Items?IncludeItemTypes=Movie&Genres=Action&OfficialRatings=R&Years=2024&NameStartsWith=Hi", head, http.StatusOK)
	if count(filtered) != 1 || firstID(filtered) != "m2" {
		t.Fatalf("bad Jellyfin item filters: %#v", filtered)
	}
	post(t, h, "/Sessions/Playing/Progress", head, []byte(`{"ItemId":"m2","PositionTicks":90}`), http.StatusNoContent)
	item := get(t, h, "/Items/m2", head, http.StatusOK)
	if item["UserData"].(map[string]any)["PlaybackPositionTicks"].(float64) != 90 {
		t.Fatalf("bad user data: %#v", item["UserData"])
	}
}

func TestSearchHintsRankAndFields(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	must(t, err)
	defer s.Close()
	must(t, s.SaveLibraries([]store.Library{{ID: "lib", Name: "Movies", Type: "movies", Path: dir}}))
	must(t, s.UpsertItem(store.Item{ID: "m1", LibraryID: "lib", ParentID: "lib", Type: "Movie", Name: "The Dune", ProductionYear: 1984}))
	must(t, s.UpsertItem(store.Item{ID: "m2", LibraryID: "lib", ParentID: "lib", Type: "Movie", Name: "Dune", ProductionYear: 2021}))
	must(t, s.UpsertImage(store.Image{ItemID: "m2", Type: "Primary", Path: "poster.jpg", Tag: "poster-tag"}))

	h := API{C: config.Config{Server: config.Server{ID: "server-1"}}, S: s}.Handler()
	hints := get(t, h, "/Search/Hints?SearchTerm=dune", nil, http.StatusOK)["SearchHints"].([]any)
	if len(hints) != 2 {
		t.Fatalf("hint count = %d", len(hints))
	}
	first := hints[0].(map[string]any)
	if first["Name"] != "Dune" || first["PrimaryImageTag"] != "poster-tag" || first["ProductionYear"].(float64) != 2021 {
		t.Fatalf("bad first hint: %#v", first)
	}
}

func TestPhase5UserSessionAndNextUp(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	must(t, err)
	defer s.Close()
	must(t, s.SaveLibraries([]store.Library{{ID: "tv", Name: "TV", Type: "tvshows", Path: dir}}))
	must(t, s.AddUserPolicy("admin", "pass", true, false, 0))
	must(t, s.AddUserPolicy("other", "pass", true, false, 0))
	must(t, s.UpsertItem(store.Item{ID: "another-show", LibraryID: "tv", ParentID: "tv", Type: "Series", Name: "Another Show", SortName: "another show", IsFolder: true}))
	must(t, s.UpsertItem(store.Item{ID: "another-season", LibraryID: "tv", ParentID: "another-show", Type: "Season", Name: "Season 1", IsFolder: true, IndexNumber: 1}))
	must(t, s.UpsertItem(store.Item{ID: "a1", LibraryID: "tv", ParentID: "another-season", Type: "Episode", Name: "A Episode", SortName: "a episode", IndexNumber: 1, ParentIndexNumber: 1, RuntimeTicks: 100}))
	must(t, s.UpsertItem(store.Item{ID: "a2", LibraryID: "tv", ParentID: "another-season", Type: "Episode", Name: "B Episode", SortName: "b episode", IndexNumber: 2, ParentIndexNumber: 1, RuntimeTicks: 100}))
	must(t, s.UpsertItem(store.Item{ID: "show", LibraryID: "tv", ParentID: "tv", Type: "Series", Name: "Show", IsFolder: true}))
	must(t, s.UpsertItem(store.Item{ID: "season", LibraryID: "tv", ParentID: "show", Type: "Season", Name: "Season 1", IsFolder: true, IndexNumber: 1}))
	must(t, s.UpsertItem(store.Item{ID: "e1", LibraryID: "tv", ParentID: "season", Type: "Episode", Name: "Episode 1", IndexNumber: 1, ParentIndexNumber: 1, RuntimeTicks: 100}))
	must(t, s.UpsertItem(store.Item{ID: "e2", LibraryID: "tv", ParentID: "season", Type: "Episode", Name: "Episode 2", IndexNumber: 2, ParentIndexNumber: 1, RuntimeTicks: 100}))
	h := API{C: config.Config{Server: config.Server{ID: "server-1"}}, S: s}.Handler()

	status(t, h, http.MethodGet, "/Sessions", nil, nil, http.StatusUnauthorized)
	auth := post(t, h, "/Users/AuthenticateByName", map[string]string{"Authorization": `MediaBrowser Client="Plezy", Device="Phone", DeviceId="dev1"`}, []byte(`{"Username":"admin","Pw":"pass"}`), http.StatusOK)
	other := post(t, h, "/Users/AuthenticateByName", map[string]string{"Authorization": `MediaBrowser Client="Plezy", Device="Tablet", DeviceId="dev2"`}, []byte(`{"Username":"other","Pw":"pass"}`), http.StatusOK)
	head := map[string]string{"X-Emby-Token": auth["AccessToken"].(string)}
	otherHead := map[string]string{"X-Emby-Token": other["AccessToken"].(string)}
	sessions := get(t, h, "/Sessions", head, http.StatusOK)["Items"].([]any)
	if len(sessions) != 1 || sessions[0].(map[string]any)["DeviceName"] != "Phone" {
		t.Fatalf("bad sessions: %#v", sessions)
	}
	otherSessions := get(t, h, "/Sessions", otherHead, http.StatusOK)["Items"].([]any)
	if len(otherSessions) != 1 || otherSessions[0].(map[string]any)["DeviceName"] != "Tablet" {
		t.Fatalf("bad session isolation: %#v", otherSessions)
	}

	post(t, h, "/Sessions/Playing/Stopped", head, []byte(`{"ItemId":"e1","PositionTicks":95}`), http.StatusNoContent)
	post(t, h, "/Sessions/Playing/Stopped", head, []byte(`{"ItemId":"e1","PositionTicks":95}`), http.StatusNoContent)
	item := get(t, h, "/Items/e1", head, http.StatusOK)
	ud := item["UserData"].(map[string]any)
	if ud["Played"] != true || ud["PlayCount"].(float64) != 1 {
		t.Fatalf("bad watched state: %#v", ud)
	}
	next := get(t, h, "/Shows/NextUp?Limit=10", head, http.StatusOK)
	if count(next) != 2 || firstID(next) != "a1" || next["Items"].([]any)[1].(map[string]any)["Id"] != "e2" {
		t.Fatalf("bad next up: %#v", next)
	}
	nextShow := get(t, h, "/Shows/NextUp?SeriesId=show", head, http.StatusOK)
	if count(nextShow) != 1 || firstID(nextShow) != "e2" {
		t.Fatalf("bad series next up: %#v", nextShow)
	}
	post(t, h, "/Sessions/Logout", head, nil, http.StatusNoContent)
	status(t, h, http.MethodGet, "/Sessions", head, nil, http.StatusUnauthorized)
}

func TestFavoritesAndChildPolicy(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	must(t, err)
	defer s.Close()
	must(t, s.SaveLibraries([]store.Library{{ID: "lib", Name: "Movies", Type: "movies", Path: dir}}))
	must(t, s.AddUserPolicy("parent", "pass", true, false, 0))
	must(t, s.AddUserPolicy("child", "pass", false, true, 3))
	okPath := filepath.Join(dir, "ok.mp4")
	blockedPath := filepath.Join(dir, "blocked.mp4")
	must(t, os.WriteFile(okPath, []byte("ok"), 0644))
	must(t, os.WriteFile(blockedPath, []byte("blocked"), 0644))
	must(t, s.UpsertItem(store.Item{ID: "ok", LibraryID: "lib", ParentID: "lib", Type: "Movie", Name: "Allowed", Path: okPath, Container: "mp4", OfficialRating: "PG-13"}))
	must(t, s.UpsertItem(store.Item{ID: "blocked", LibraryID: "lib", ParentID: "lib", Type: "Movie", Name: "Blocked", Path: blockedPath, Container: "mp4", OfficialRating: "R", PeopleJSON: `[{"Name":"Blocked Actor","Type":"Actor"}]`}))
	must(t, s.SavePeople("blocked", []store.Person{{Name: "Blocked Actor", Role: "Actor"}}))
	h := API{C: config.Config{Server: config.Server{ID: "server-1"}}, S: s}.Handler()

	parent := post(t, h, "/Users/AuthenticateByName", nil, []byte(`{"Username":"parent","Pw":"pass"}`), http.StatusOK)
	parentHead := map[string]string{"X-Emby-Token": parent["AccessToken"].(string)}
	post(t, h, "/UserFavoriteItems/blocked", parentHead, nil, http.StatusOK)
	item := get(t, h, "/Items/blocked", parentHead, http.StatusOK)
	if item["UserData"].(map[string]any)["IsFavorite"] != true {
		t.Fatalf("favorite not set: %#v", item["UserData"])
	}
	favs := get(t, h, "/Items?IncludeItemTypes=Movie&Filters=IsFavorite", parentHead, http.StatusOK)
	if count(favs) != 1 || firstID(favs) != "blocked" {
		t.Fatalf("bad favorite filter: %#v", favs)
	}
	req := httptest.NewRequest(http.MethodDelete, "/UserFavoriteItems/blocked", nil)
	req.Header.Set("X-Emby-Token", parentHead["X-Emby-Token"])
	do(t, h, req, http.StatusOK)
	item = get(t, h, "/Items/blocked", parentHead, http.StatusOK)
	if item["UserData"].(map[string]any)["IsFavorite"] != false {
		t.Fatalf("favorite not cleared: %#v", item["UserData"])
	}

	childAuth := post(t, h, "/Users/AuthenticateByName", nil, []byte(`{"Username":"child","Pw":"pass"}`), http.StatusOK)
	if childAuth["User"].(map[string]any)["Policy"].(map[string]any)["MaxParentalRating"].(float64) != 3 {
		t.Fatalf("bad child policy: %#v", childAuth["User"])
	}
	childHead := map[string]string{"X-Emby-Token": childAuth["AccessToken"].(string)}
	page := get(t, h, "/Items?IncludeItemTypes=Movie", childHead, http.StatusOK)
	if count(page) != 1 || firstID(page) != "ok" {
		t.Fatalf("child saw blocked item: %#v", page)
	}
	counts := get(t, h, "/Items/Counts", childHead, http.StatusOK)
	if counts["MovieCount"].(float64) != 1 {
		t.Fatalf("child counts included blocked item: %#v", counts)
	}
	filters := get(t, h, "/Items/Filters", childHead, http.StatusOK)
	if len(filters["OfficialRatings"].([]any)) != 1 || filters["OfficialRatings"].([]any)[0] != "PG-13" {
		t.Fatalf("child filters included blocked rating: %#v", filters)
	}
	status(t, h, http.MethodGet, "/Items/blocked", childHead, nil, http.StatusNotFound)
	status(t, h, http.MethodPost, "/Items/blocked/PlaybackInfo", childHead, []byte(`{}`), http.StatusForbidden)
	hints := get(t, h, "/Search/Hints?SearchTerm=Blocked", childHead, http.StatusOK)["SearchHints"].([]any)
	if len(hints) != 0 {
		t.Fatalf("child search saw blocked item: %#v", hints)
	}
	people := get(t, h, "/Persons?SearchTerm=Blocked", childHead, http.StatusOK)
	if count(people) != 0 {
		t.Fatalf("child people saw blocked person: %#v", people)
	}
	ratings := get(t, h, "/Localization/ParentalRatings", childHead, http.StatusOK)["Items"].([]any)
	if len(ratings) == 0 {
		t.Fatal("missing parental ratings")
	}
	stream := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/Videos/blocked/stream?api_key="+childHead["X-Emby-Token"], nil)
	h.ServeHTTP(stream, req)
	if stream.Code != http.StatusForbidden {
		t.Fatalf("child stream = %d, want 403", stream.Code)
	}
}

func TestPlezyPhase6Endpoints(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "gofin.db"))
	must(t, err)
	defer s.Close()
	must(t, s.SaveLibraries([]store.Library{{ID: "tv", Name: "TV", Type: "tvshows", Path: dir}}))
	must(t, s.AddUserPolicy("admin", "pass", true, false, 0))
	must(t, s.UpsertItem(store.Item{ID: "show", LibraryID: "tv", ParentID: "tv", Type: "Series", Name: "Show", IsFolder: true}))
	must(t, s.UpsertItem(store.Item{ID: "season", LibraryID: "tv", ParentID: "show", Type: "Season", Name: "Season 1", IsFolder: true}))
	must(t, s.UpsertItem(store.Item{ID: "e1", LibraryID: "tv", ParentID: "season", Type: "Episode", Name: "Episode 1"}))
	must(t, s.UpsertItem(store.Item{ID: "e2", LibraryID: "tv", ParentID: "season", Type: "Episode", Name: "Episode 2"}))
	h := API{C: config.Config{Server: config.Server{ID: "server-1"}}, S: s}.Handler()
	auth := post(t, h, "/Users/AuthenticateByName", nil, []byte(`{"Username":"admin","Pw":"pass"}`), http.StatusOK)
	userID := auth["User"].(map[string]any)["Id"].(string)
	head := map[string]string{"X-Emby-Token": auth["AccessToken"].(string)}

	eps := get(t, h, "/Shows/show/Episodes?userId="+userID+"&SeasonId=season&StartIndex=0&Limit=1", head, http.StatusOK)
	if count(eps) != 1 || firstID(eps) != "e1" {
		t.Fatalf("bad Plezy season episode page: %#v", eps)
	}
	post(t, h, "/UserPlayedItems/e1?userId="+userID, head, nil, http.StatusOK)
	played := get(t, h, "/Items?ParentId=season&IncludeItemTypes=Episode&Filters=IsPlayed", head, http.StatusOK)
	if count(played) != 1 || firstID(played) != "e1" {
		t.Fatalf("bad played filter: %#v", played)
	}
	status(t, h, http.MethodDelete, "/UserPlayedItems/e1?userId="+userID, head, nil, http.StatusOK)
	unplayed := get(t, h, "/Items?ParentId=season&IncludeItemTypes=Episode&Filters=IsUnplayed", head, http.StatusOK)
	if count(unplayed) != 2 {
		t.Fatalf("bad unplayed filter: %#v", unplayed)
	}
	rated := post(t, h, "/UserItems/e1/Rating?userId="+userID+"&Likes=true", head, nil, http.StatusOK)
	if rated["Likes"] != true {
		t.Fatalf("bad rating set: %#v", rated)
	}
	status(t, h, http.MethodDelete, "/UserItems/e1/Rating?userId="+userID, head, nil, http.StatusOK)
	post(t, h, "/Users/"+userID+"/FavoriteItems/e2", head, nil, http.StatusOK)
	favs := get(t, h, "/Items?ParentId=season&IncludeItemTypes=Episode&Filters=IsFavorite", head, http.StatusOK)
	if count(favs) != 1 || firstID(favs) != "e2" {
		t.Fatalf("bad Plezy favorite alias: %#v", favs)
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
	must(t, s.AddUserPolicy("admin", "pass", true, false, 0))
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
	source := pb["MediaSources"].([]any)[0].(map[string]any)
	if source["Size"].(float64) != float64(len("fake-video")) || source["Container"] != "mp4" || source["SupportsTranscoding"] != false {
		t.Fatalf("bad media source: %#v", source)
	}
	if source["MediaSourceId"] != source["Id"] || !strings.Contains(source["DirectStreamUrl"].(string), "MediaSourceId=") || pb["SupportsDirectPlay"] != true {
		t.Fatalf("bad playback info: %#v", pb)
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
	segments := get(t, h, "/MediaSegments/"+movieID, head, http.StatusOK)
	if count(segments) != 0 {
		t.Fatalf("media segments should be empty: %#v", segments)
	}
	live := get(t, h, "/LiveTv/Channels?limit=1", head, http.StatusOK)
	if count(live) != 0 {
		t.Fatalf("live tv should be empty: %#v", live)
	}
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

func status(t *testing.T, h http.Handler, method, path string, headers map[string]string, body []byte, want int) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, w.Code, want, w.Body.String())
	}
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
