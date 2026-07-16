package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gofin/internal/config"
	"gofin/internal/metadata"
	"gofin/internal/store"
)

type API struct {
	C config.Config
	S *store.Store
}

func (a API) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/System/Info/Public", a.systemInfoPublic)
	m.HandleFunc("/System/Info", a.systemInfo)
	m.HandleFunc("/QuickConnect/Enabled", a.quickConnect)
	m.HandleFunc("/Branding/Configuration", a.branding)
	m.HandleFunc("/Branding/Css", a.css)
	m.HandleFunc("/DisplayPreferences/usersettings", a.displayPrefs)
	m.HandleFunc("/Localization/ParentalRatings", a.parentalRatings)
	m.HandleFunc("/Users/Public", a.usersPublic)
	m.HandleFunc("/Users/AuthenticateByName", a.auth)
	m.HandleFunc("/Users/Me", a.me)
	m.HandleFunc("/UserViews", a.userViews)
	m.HandleFunc("/Library/MediaFolders", a.userViews)
	m.HandleFunc("/Items/Latest", a.latest)
	m.HandleFunc("/Items/Counts", a.counts)
	m.HandleFunc("/Items/Filters", a.filters)
	m.HandleFunc("/Items", a.items)
	m.HandleFunc("/Items/", a.item)
	m.HandleFunc("/LiveTv/Channels", a.emptyPage)
	m.HandleFunc("/LiveTv/Programs", a.emptyPage)
	m.HandleFunc("/UserFavoriteItems/", a.favorite)
	m.HandleFunc("/Shows/NextUp", a.nextUp)
	m.HandleFunc("/Shows/", a.shows)
	m.HandleFunc("/UserItems/Resume", a.resume)
	m.HandleFunc("/UserItems/", a.rating)
	m.HandleFunc("/UserPlayedItems/", a.playedItems)
	m.HandleFunc("/Search/Hints", a.searchHints)
	m.HandleFunc("/MediaSegments/", a.mediaSegments)
	m.HandleFunc("/Sessions", a.sessions)
	m.HandleFunc("/Sessions/", a.sessions)
	m.HandleFunc("/Persons", a.personsList)
	m.HandleFunc("/Persons/", a.persons)
	m.HandleFunc("/Videos/", a.video)
	m.HandleFunc("/Users/", a.usersCompat)
	return log(m)
}

func (a API) systemInfo(w http.ResponseWriter, r *http.Request)       { write(w, a.info()) }
func (a API) systemInfoPublic(w http.ResponseWriter, r *http.Request) { write(w, a.info()) }
func (a API) quickConnect(w http.ResponseWriter, r *http.Request)     { write(w, false) }
func (a API) branding(w http.ResponseWriter, r *http.Request)         { write(w, map[string]any{}) }
func (a API) css(w http.ResponseWriter, r *http.Request)              { w.Header().Set("Content-Type", "text/css") }
func (a API) displayPrefs(w http.ResponseWriter, r *http.Request)     { write(w, map[string]any{}) }
func (a API) parentalRatings(w http.ResponseWriter, r *http.Request)  { write(w, parentalRatings()) }
func (a API) info() map[string]any {
	return map[string]any{"ServerName": a.C.Server.Name, "Id": a.C.Server.ID, "LocalAddress": a.C.Server.PublicURL, "Version": "10.10.0", "ProductName": "GoFin", "OperatingSystem": "Linux", "StartupWizardCompleted": true}
}

func (a API) usersPublic(w http.ResponseWriter, r *http.Request) {
	users, err := a.S.PublicUsers()
	if err != nil {
		fail(w, err, 500)
		return
	}
	out := []map[string]any{}
	for _, u := range users {
		out = append(out, userDTO(u, a.C.Server.ID))
	}
	write(w, out)
}

func (a API) auth(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Pw, Password string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, err, 400)
		return
	}
	pass := in.Pw
	if pass == "" {
		pass = in.Password
	}
	deviceID, deviceName, client := authPart(r, "DeviceId"), authPart(r, "Device"), authPart(r, "Client")
	u, tok, err := a.S.AuthDevice(in.Username, pass, deviceID, deviceName, client)
	if err != nil {
		fail(w, err, 401)
		return
	}
	write(w, map[string]any{"User": userDTO(u, a.C.Server.ID), "AccessToken": tok, "ServerId": a.C.Server.ID, "SessionInfo": sessionDTO(store.Session{ID: store.StableID("session", u.ID, tok), UserID: u.ID, UserName: u.Name, DeviceID: deviceID, DeviceName: deviceName, Client: client})})
}

func (a API) me(w http.ResponseWriter, r *http.Request) {
	u, ok := a.user(w, r)
	if ok {
		write(w, userDTO(u, a.C.Server.ID))
	}
}

func (a API) userViews(w http.ResponseWriter, r *http.Request) {
	libs, err := a.S.Libraries()
	if err != nil {
		fail(w, err, 500)
		return
	}
	items := []map[string]any{}
	for _, l := range libs {
		items = append(items, map[string]any{"Id": l.ID, "Name": l.Name, "ServerId": a.C.Server.ID, "Type": "CollectionFolder", "IsFolder": true, "CollectionType": jfType(l.Type), "ImageTags": map[string]string{}})
	}
	write(w, page(items))
}

func (a API) items(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	u, _ := a.userNoFail(r)
	items, err := a.S.Items(store.ItemQuery{ParentID: q.Get("ParentId"), Type: q.Get("IncludeItemTypes"), Search: q.Get("SearchTerm"), SortBy: q.Get("SortBy"), SortOrder: q.Get("SortOrder"), PersonIDs: q.Get("PersonIds"), Genres: q.Get("Genres"), OfficialRatings: q.Get("OfficialRatings"), Years: q.Get("Years"), NameStartsWith: q.Get("NameStartsWith"), UserID: u.ID, Favorite: userFilter(q, "IsFavorite"), Played: userFilter(q, "IsPlayed"), Unplayed: userFilter(q, "IsUnplayed"), Recursive: parseBool(q.Get("Recursive")), Start: atoi(q.Get("StartIndex")), Limit: atoi(q.Get("Limit"))})
	if err != nil {
		fail(w, err, 500)
		return
	}
	items = a.allowedItems(u, items)
	write(w, page(a.itemDTOs(items, u.ID)))
}

func (a API) allowedItems(u store.User, items []store.Item) []store.Item {
	out := make([]store.Item, 0, len(items))
	for _, it := range items {
		if a.allowed(u, it) {
			out = append(out, it)
		}
	}
	return out
}

func (a API) itemDTOs(items []store.Item, userID string) []map[string]any {
	ids := itemIDs(items)
	imgs, _ := a.S.ImagesByItemIDs(ids)
	playback, _ := a.S.PlaybackByItemIDs(userID, ids)
	parents := a.episodeParents(items)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, a.itemDTOWith(it, imgs[it.ID], playback[it.ID], parents[it.ID]))
	}
	return out
}

type episodeParent struct {
	season store.Item
	series store.Item
	images []store.Image
}

func (a API) episodeParents(items []store.Item) map[string]*episodeParent {
	seasonIDs := make([]string, 0)
	seenSeasons := map[string]bool{}
	for _, item := range items {
		if item.Type == "Episode" && item.ParentID != "" && !seenSeasons[item.ParentID] {
			seenSeasons[item.ParentID] = true
			seasonIDs = append(seasonIDs, item.ParentID)
		}
	}
	seasons, err := a.S.ItemsByIDs(seasonIDs)
	if err != nil {
		return map[string]*episodeParent{}
	}
	seriesIDs := make([]string, 0)
	seenSeries := map[string]bool{}
	for _, season := range seasons {
		if season.ParentID != "" && !seenSeries[season.ParentID] {
			seenSeries[season.ParentID] = true
			seriesIDs = append(seriesIDs, season.ParentID)
		}
	}
	series, err := a.S.ItemsByIDs(seriesIDs)
	if err != nil {
		return map[string]*episodeParent{}
	}
	images, err := a.S.ImagesByItemIDs(seriesIDs)
	if err != nil {
		return map[string]*episodeParent{}
	}
	out := map[string]*episodeParent{}
	for _, item := range items {
		season, ok := seasons[item.ParentID]
		if !ok {
			continue
		}
		series, ok := series[season.ParentID]
		if !ok || series.Type != "Series" {
			continue
		}
		out[item.ID] = &episodeParent{season: season, series: series, images: images[series.ID]}
	}
	return out
}

func itemIDs(items []store.Item) []string {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

func primaryImageTags(imgs map[string][]store.Image) map[string]string {
	out := map[string]string{}
	for id, list := range imgs {
		for _, img := range list {
			if img.Type == "Primary" {
				out[id] = img.Tag
				break
			}
		}
	}
	return out
}

func (a API) item(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/Items/")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) > 1 {
		switch parts[1] {
		case "PlaybackInfo":
			a.playbackInfo(w, r, id)
			return
		case "Images":
			a.images(w, r, id, parts[2:])
			return
		case "Similar", "LocalTrailers", "SpecialFeatures":
			write(w, page([]map[string]any{}))
			return
		case "Refresh":
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	if r.Method == http.MethodPost {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	it, err := a.S.Item(id)
	if err != nil {
		fail(w, err, 404)
		return
	}
	u, _ := a.userNoFail(r)
	if !a.allowed(u, it) {
		http.NotFound(w, r)
		return
	}
	write(w, a.itemDTO(it, u.ID))
}

func (a API) playbackInfo(w http.ResponseWriter, r *http.Request, id string) {
	it, err := a.S.Item(id)
	if err != nil {
		fail(w, err, 404)
		return
	}
	u, _ := a.userNoFail(r)
	if !a.allowed(u, it) {
		fail(w, errors.New("forbidden"), http.StatusForbidden)
		return
	}
	dto := a.itemDTO(it, u.ID)
	write(w, map[string]any{"MediaSources": dto["MediaSources"], "PlaySessionId": store.StableID("play", id), "ErrorCode": "", "SupportsDirectPlay": true, "SupportsDirectStream": true, "SupportsTranscoding": false})
}

func (a API) images(w http.ResponseWriter, r *http.Request, id string, parts []string) {
	u, _ := a.userNoFail(r)
	if it, err := a.S.Item(id); err == nil && !a.allowed(u, it) {
		http.NotFound(w, r)
		return
	}
	imgs, err := a.S.Images(id)
	if err != nil {
		fail(w, err, 500)
		return
	}
	if len(parts) == 0 {
		out := []map[string]any{}
		for _, img := range imgs {
			out = append(out, map[string]any{"ImageType": img.Type, "ImageIndex": img.Index, "ImageTag": img.Tag, "Path": img.Path})
		}
		write(w, out)
		return
	}
	typ := parts[0]
	idx := 0
	if len(parts) > 1 {
		idx = atoi(parts[1])
	}
	for _, img := range imgs {
		if strings.EqualFold(img.Type, typ) && img.Index == idx {
			if strings.HasPrefix(img.Path, "http://") || strings.HasPrefix(img.Path, "https://") {
				http.Redirect(w, r, img.Path, http.StatusFound)
				return
			}
			http.ServeFile(w, r, img.Path)
			return
		}
	}
	if strings.EqualFold(typ, "Primary") {
		if img, _ := a.S.PersonImageByID(id); img != "" {
			http.Redirect(w, r, img, http.StatusFound)
			return
		}
	}
	http.NotFound(w, r)
}

func (a API) latest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	u, _ := a.userNoFail(r)
	parentID := q.Get("ParentId")
	query := store.ItemQuery{ParentID: parentID, Type: q.Get("IncludeItemTypes"), SortBy: "DateCreated", SortOrder: "Descending", Recursive: true, Limit: firstInt(q.Get("Limit"), 16)}
	if a.isTVLibrary(parentID) {
		// A TV library's home row should show recently added shows, not one
		// card per episode. Opening a series exposes its seasons and episodes.
		query.Type = "Series"
		query.Recursive = false
	}
	items, err := a.S.Items(query)
	if err != nil {
		fail(w, err, 500)
		return
	}
	var latest []store.Item
	for _, it := range a.allowedItems(u, items) {
		if it.Type != "CollectionFolder" && (!it.IsFolder || it.Type == "Series") {
			latest = append(latest, it)
		}
	}
	write(w, a.itemDTOs(latest, u.ID))
}

func (a API) isTVLibrary(id string) bool {
	if id == "" {
		return false
	}
	libraries, err := a.S.Libraries()
	if err != nil {
		return false
	}
	for _, library := range libraries {
		if library.ID == id {
			return library.Type == "tvshows"
		}
	}
	return false
}

func (a API) resume(w http.ResponseWriter, r *http.Request) {
	u, _ := a.userNoFail(r)
	q := r.URL.Query()
	items, err := a.S.Resume(u.ID, q.Get("ParentId"), q.Get("IncludeItemTypes"), firstInt(q.Get("Limit"), 16))
	if err != nil {
		fail(w, err, 500)
		return
	}
	write(w, page(a.itemDTOs(a.allowedItems(u, items), u.ID)))
}

func (a API) shows(w http.ResponseWriter, r *http.Request) {
	u, _ := a.userNoFail(r)
	urlq := r.URL.Query()
	p := strings.TrimPrefix(r.URL.Path, "/Shows/")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	q := store.ItemQuery{ParentID: parts[0], Start: atoi(urlq.Get("StartIndex")), Limit: atoi(urlq.Get("Limit")), SortBy: "IndexNumber"}
	if parts[1] == "Seasons" {
		q.Type = "Season"
	}
	if parts[1] == "Episodes" && urlq.Get("SeasonId") != "" {
		q.ParentID = urlq.Get("SeasonId")
		q.Type = "Episode"
	}
	if parts[1] == "Episodes" && urlq.Get("SeasonId") == "" {
		q.Type = "Season"
		q.Start = 0
		q.Limit = 0
	}
	items, err := a.S.Items(q)
	if err != nil {
		fail(w, err, 500)
		return
	}
	if parts[1] == "Episodes" && urlq.Get("SeasonId") == "" {
		var eps []store.Item
		for _, season := range items {
			children, err := a.S.Items(store.ItemQuery{ParentID: season.ID, Type: "Episode", SortBy: "IndexNumber"})
			if err != nil {
				fail(w, err, 500)
				return
			}
			eps = append(eps, children...)
		}
		items = eps
		items = pagedItems(items, atoi(urlq.Get("StartIndex")), atoi(urlq.Get("Limit")))
	}
	write(w, page(a.itemDTOs(a.allowedItems(u, items), u.ID)))
}

func pagedItems(items []store.Item, start, limit int) []store.Item {
	if start >= len(items) {
		return nil
	}
	if start > 0 {
		items = items[start:]
	}
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func (a API) nextUp(w http.ResponseWriter, r *http.Request) {
	u, _ := a.userNoFail(r)
	q := r.URL.Query()
	items, err := a.S.NextUp(u.ID, q.Get("SeriesId"), q.Get("ParentId"), atoi(q.Get("StartIndex")), firstInt(q.Get("Limit"), 16))
	if err != nil {
		fail(w, err, 500)
		return
	}
	write(w, page(a.itemDTOs(a.allowedItems(u, items), u.ID)))
}
func (a API) counts(w http.ResponseWriter, r *http.Request) {
	u, _ := a.userNoFail(r)
	if u.ID == "" || u.IsAdmin || !u.IsChild || u.MaxParentalRating == 0 {
		movies, _ := a.S.CountItemsByType("Movie")
		series, _ := a.S.CountItemsByType("Series")
		episodes, _ := a.S.CountItemsByType("Episode")
		write(w, map[string]int{"MovieCount": movies, "SeriesCount": series, "EpisodeCount": episodes})
		return
	}
	movies, _ := a.S.Items(store.ItemQuery{Type: "Movie"})
	series, _ := a.S.Items(store.ItemQuery{Type: "Series"})
	episodes, _ := a.S.Items(store.ItemQuery{Type: "Episode"})
	write(w, map[string]int{"MovieCount": a.allowedCount(u, movies), "SeriesCount": a.allowedCount(u, series), "EpisodeCount": a.allowedCount(u, episodes)})
}
func (a API) filters(w http.ResponseWriter, r *http.Request) {
	u, _ := a.userNoFail(r)
	if !u.IsChild {
		genres, ratings, years, err := a.S.Filters()
		if err != nil {
			fail(w, err, 500)
			return
		}
		write(w, map[string]any{"Genres": genres, "Tags": []string{}, "OfficialRatings": ratings, "Years": years})
		return
	}
	items, err := a.S.Items(store.ItemQuery{Type: "Movie,Series,Episode"})
	if err != nil {
		fail(w, err, 500)
		return
	}
	write(w, map[string]any{"Genres": a.filterGenres(u, items), "Tags": []string{}, "OfficialRatings": a.filterRatings(u, items), "Years": a.filterYears(u, items)})
}
func (a API) searchHints(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	term := q.Get("SearchTerm")
	limit := firstInt(q.Get("Limit"), 20)
	u, _ := a.userNoFail(r)
	out := []map[string]any{}
	if term != "" {
		items, _ := a.S.Items(store.ItemQuery{Search: term, Type: q.Get("IncludeItemTypes"), Limit: limit})
		sort.SliceStable(items, func(i, j int) bool { return hintRank(items[i].Name, term) < hintRank(items[j].Name, term) })
		imgs, _ := a.S.ImagesByItemIDs(itemIDs(items))
		tags := primaryImageTags(imgs)
		for _, it := range items {
			if !a.allowed(u, it) {
				continue
			}
			out = append(out, itemHint(it, tags[it.ID]))
		}
		people, _ := a.S.People(store.PersonQuery{Search: term, Limit: limit - len(out)})
		sort.SliceStable(people, func(i, j int) bool { return hintRank(people[i].Name, term) < hintRank(people[j].Name, term) })
		for _, p := range people {
			if !a.personAllowed(u, p.ID) {
				continue
			}
			out = append(out, personHint(p))
		}
	}
	write(w, map[string]any{"SearchHints": out, "TotalRecordCount": len(out)})
}
func (a API) mediaSegments(w http.ResponseWriter, r *http.Request) {
	write(w, map[string]any{"Items": []any{}, "TotalRecordCount": 0})
}

func (a API) emptyPage(w http.ResponseWriter, r *http.Request) { write(w, page([]map[string]any{})) }

func (a API) favorite(w http.ResponseWriter, r *http.Request) {
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/UserFavoriteItems/"), "/")
	a.setFavorite(w, r, u, id)
}

func (a API) setFavorite(w http.ResponseWriter, r *http.Request, u store.User, id string) {
	it, err := a.S.Item(id)
	if err != nil {
		fail(w, err, http.StatusNotFound)
		return
	}
	if !a.allowed(u, it) {
		fail(w, errors.New("forbidden"), http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodPost, http.MethodDelete:
		if err := a.S.SetFavorite(u.ID, id, r.Method == http.MethodPost); err != nil {
			fail(w, err, http.StatusInternalServerError)
			return
		}
		write(w, userData(a.S.Playback(u.ID, id), id))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a API) rating(w http.ResponseWriter, r *http.Request) {
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/UserItems/"), "/")
	if !strings.HasSuffix(id, "/Rating") {
		http.NotFound(w, r)
		return
	}
	id = strings.TrimSuffix(id, "/Rating")
	if it, err := a.S.Item(id); err != nil {
		fail(w, err, http.StatusNotFound)
		return
	} else if !a.allowed(u, it) {
		fail(w, errors.New("forbidden"), http.StatusForbidden)
		return
	}
	var likes *bool
	if r.Method == http.MethodPost {
		v := parseBool(r.URL.Query().Get("Likes"))
		likes = &v
	} else if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := a.S.SetRating(u.ID, id, likes); err != nil {
		fail(w, err, http.StatusInternalServerError)
		return
	}
	write(w, userData(a.S.Playback(u.ID, id), id))
}

func (a API) playedItems(w http.ResponseWriter, r *http.Request) {
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/UserPlayedItems/"), "/")
	it, err := a.S.Item(id)
	if err != nil {
		fail(w, err, http.StatusNotFound)
		return
	}
	if !a.allowed(u, it) {
		fail(w, errors.New("forbidden"), http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodPost:
		_ = a.S.SaveProgress(u.ID, id, 0, true)
	case http.MethodDelete:
		_ = a.S.SaveProgress(u.ID, id, 0, false)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	write(w, userData(a.S.Playback(u.ID, id), id))
}

func (a API) sessions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		u, ok := a.user(w, r)
		if !ok {
			return
		}
		sessions, err := a.S.Sessions(u.ID)
		if err != nil {
			fail(w, err, 500)
			return
		}
		out := []map[string]any{}
		for _, ss := range sessions {
			out = append(out, sessionDTO(ss))
		}
		write(w, out)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/Logout") {
		if _, ok := a.user(w, r); !ok {
			return
		}
		_ = a.S.DeleteToken(token(r))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if strings.Contains(r.URL.Path, "/Playing") {
		var in struct {
			ItemId        string
			PositionTicks int64
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if u, ok := a.userNoFail(r); ok && in.ItemId != "" {
			played := false
			if strings.HasSuffix(r.URL.Path, "/Stopped") {
				if it, err := a.S.Item(in.ItemId); err == nil && it.RuntimeTicks > 0 {
					played = in.PositionTicks >= it.RuntimeTicks*9/10
				} else {
					played = in.PositionTicks == 0
				}
			}
			_ = a.S.SaveProgress(u.ID, in.ItemId, in.PositionTicks, played)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a API) video(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/Videos/")
	if i := strings.Index(id, "/"); i >= 0 {
		id = id[:i]
	}
	if i := strings.Index(id, "."); i >= 0 {
		id = id[:i]
	}
	it, err := a.S.Item(id)
	if err != nil {
		fail(w, err, 404)
		return
	}
	if u, ok := a.userNoFail(r); ok && !a.allowed(u, it) {
		fail(w, errors.New("forbidden"), http.StatusForbidden)
		return
	}
	http.ServeFile(w, r, it.Path)
}

func (a API) persons(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/Persons/")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) == 1 && parts[0] != "" {
		name, err := url.PathUnescape(parts[0])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		p, err := a.person(name)
		if err != nil {
			fail(w, err, 404)
			return
		}
		u, _ := a.userNoFail(r)
		if !a.personAllowed(u, p.ID) {
			http.NotFound(w, r)
			return
		}
		write(w, a.personDTO(p, true))
		return
	}
	if len(parts) < 3 || parts[1] != "Images" {
		http.NotFound(w, r)
		return
	}
	u, err := url.PathUnescape(parts[0])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if img, _ := a.S.PersonImage(u); img != "" {
		http.Redirect(w, r, img, http.StatusFound)
		return
	}
	http.NotFound(w, r)
}

func (a API) personsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	people, err := a.S.People(store.PersonQuery{Search: q.Get("SearchTerm"), StartsWith: q.Get("NameStartsWith"), AppearsInItemID: q.Get("AppearsInItemId"), Types: q.Get("PersonTypes"), ExcludeTypes: q.Get("ExcludePersonTypes"), Start: atoi(q.Get("StartIndex")), Limit: atoi(q.Get("Limit"))})
	if err != nil {
		fail(w, err, 500)
		return
	}
	out := []map[string]any{}
	u, _ := a.userNoFail(r)
	for _, p := range people {
		if !a.personAllowed(u, p.ID) {
			continue
		}
		out = append(out, a.personDTO(p, false))
	}
	write(w, page(out))
}

func (a API) person(name string) (store.Person, error) {
	p, err := a.S.PersonByName(name)
	if err != nil {
		return p, err
	}
	if p.TMDBID == 0 || !stale(p.UpdatedAt, 30*24*time.Hour) {
		return p, nil
	}
	if d, ok := metadata.New(a.C.Metadata).PersonDetails(p.TMDBID); ok {
		p.IMDBID, p.Biography, p.BirthDate, p.DeathDate = d.IMDBID, d.Biography, d.BirthDate, d.DeathDate
		p.PlaceOfBirth, p.KnownDepartment = d.PlaceOfBirth, d.KnownDepartment
		if d.ProfileURL != "" {
			p.ProfileURL = d.ProfileURL
		}
		_ = a.S.UpdatePersonDetails(p)
	}
	return p, nil
}

func (a API) usersCompat(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/Users/")
	if strings.HasSuffix(p, "/Views") {
		a.userViews(w, r)
		return
	}
	if strings.HasSuffix(p, "/Items/Latest") {
		a.latest(w, r)
		return
	}
	if strings.Contains(p, "/FavoriteItems/") {
		u, ok := a.user(w, r)
		if !ok {
			return
		}
		a.setFavorite(w, r, u, p[strings.LastIndex(p, "/")+1:])
		return
	}
	if strings.Contains(p, "/Items/") {
		id := p[strings.LastIndex(p, "/")+1:]
		it, err := a.S.Item(id)
		if err != nil {
			fail(w, err, 404)
			return
		}
		u, _ := a.userNoFail(r)
		if !a.allowed(u, it) {
			http.NotFound(w, r)
			return
		}
		write(w, a.itemDTO(it, u.ID))
		return
	}
	http.NotFound(w, r)
}

func (a API) itemDTO(it store.Item, userID string) map[string]any {
	imgs, _ := a.S.Images(it.ID)
	p := store.Playback{}
	if userID != "" {
		p = a.S.Playback(userID, it.ID)
	}
	return a.itemDTOWith(it, imgs, p, nil)
}

func (a API) itemDTOWith(it store.Item, imgs []store.Image, p store.Playback, parent *episodeParent) map[string]any {
	imageTags := map[string]string{}
	backdrops := []string{}
	for _, img := range imgs {
		if img.Type == "Backdrop" {
			backdrops = append(backdrops, img.Tag)
		} else {
			imageTags[img.Type] = img.Tag
		}
	}
	mediaType := "Video"
	if it.IsFolder {
		mediaType = "Unknown"
	}
	genres := stringsJSON(it.GenresJSON)
	ud := userData(p, it.ID)
	m := map[string]any{"Id": it.ID, "Name": it.Name, "ServerId": a.C.Server.ID, "Type": it.Type, "IsFolder": it.IsFolder, "ParentId": it.ParentID, "SortName": it.SortName, "DateCreated": it.DateCreated, "MediaType": mediaType, "ImageTags": imageTags, "BackdropImageTags": backdrops, "Overview": it.Overview, "ProductionYear": zeroNil(it.ProductionYear), "PremiereDate": emptyNil(it.PremiereDate), "IndexNumber": zeroNil(it.IndexNumber), "ParentIndexNumber": zeroNil(it.ParentIndexNumber), "ProviderIds": providerIDs(it.ProviderIDsJSON), "Genres": genres, "GenreItems": namedItems(genres), "Studios": namedItems(stringsJSON(it.StudiosJSON)), "People": people(it.PeopleJSON), "CommunityRating": zeroNilFloat(it.CommunityRating), "OfficialRating": emptyNil(it.OfficialRating), "RunTimeTicks": zeroNil64(it.RuntimeTicks), "Taglines": stringsJSON(it.TaglinesJSON), "ExternalUrls": externalURLs(it.ExternalURLsJSON), "MediaSourceCount": mediaSourceCount(it), "CanDownload": !it.IsFolder, "Container": emptyNil(it.Container), "Path": emptyNil(it.Path), "PrimaryImageAspectRatio": 0.6666666666666666, "UserData": ud}
	if it.Type == "Episode" {
		if parent != nil {
			addEpisodeParentImageFields(m, parent.season, parent.series, parent.images)
		} else {
			a.addEpisodeParentImages(m, it)
		}
	}
	if it.Type == "CollectionFolder" {
		m["CollectionType"] = "movies"
	}
	if !it.IsFolder {
		m["MediaSources"] = []map[string]any{a.mediaSource(it)}
		m["MediaStreams"] = []any{}
	}
	return m
}

func (a API) addEpisodeParentImages(dto map[string]any, episode store.Item) {
	season, err := a.S.Item(episode.ParentID)
	if err != nil {
		return
	}
	series, err := a.S.Item(season.ParentID)
	if err != nil || series.Type != "Series" {
		return
	}
	imgs, err := a.S.Images(series.ID)
	if err != nil {
		return
	}
	addEpisodeParentImageFields(dto, season, series, imgs)
}

func addEpisodeParentImageFields(dto map[string]any, season, series store.Item, imgs []store.Image) {
	dto["SeriesId"] = series.ID
	dto["SeriesName"] = series.Name
	dto["SeasonId"] = season.ID
	var backdrops []string
	for _, img := range imgs {
		switch img.Type {
		case "Primary":
			dto["SeriesPrimaryImageTag"] = img.Tag
		case "Backdrop":
			backdrops = append(backdrops, img.Tag)
		}
	}
	if len(backdrops) > 0 {
		dto["ParentBackdropItemId"] = series.ID
		dto["ParentBackdropImageTags"] = backdrops
	}
}

func (a API) mediaSource(it store.Item) map[string]any {
	sourceID := store.StableID("source", it.ID)
	playID := store.StableID("play", it.ID)
	url := "/Videos/" + it.ID + "/stream?Static=true&MediaSourceId=" + sourceID + "&PlaySessionId=" + playID + "&Container=" + it.Container
	return map[string]any{"Id": sourceID, "MediaSourceId": sourceID, "Path": it.Path, "Protocol": "File", "Type": "Default", "Container": it.Container, "Size": it.Size, "Name": filepath.Base(it.Path), "RunTimeTicks": zeroNil64(it.RuntimeTicks), "IsRemote": false, "SupportsDirectPlay": true, "SupportsDirectStream": true, "SupportsTranscoding": false, "MediaStreams": []any{}, "DirectStreamUrl": url}
}

func (a API) user(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	u, ok := a.userNoFail(r)
	if !ok {
		fail(w, sql.ErrNoRows, 401)
	}
	return u, ok
}
func (a API) userNoFail(r *http.Request) (store.User, bool) {
	t := token(r)
	if t == "" {
		return store.User{}, false
	}
	u, err := a.S.UserByToken(t)
	return u, err == nil
}

func token(r *http.Request) string {
	if t := r.Header.Get("X-Emby-Token"); t != "" {
		return t
	}
	if t := r.URL.Query().Get("api_key"); t != "" {
		return t
	}
	a := r.Header.Get("Authorization")
	if i := strings.Index(a, "Token=\""); i >= 0 {
		s := a[i+7:]
		if j := strings.Index(s, "\""); j >= 0 {
			return s[:j]
		}
	}
	return ""
}
func authPart(r *http.Request, key string) string {
	a := r.Header.Get("Authorization")
	if i := strings.Index(a, key+"=\""); i >= 0 {
		s := a[i+len(key)+2:]
		if j := strings.Index(s, "\""); j >= 0 {
			return s[:j]
		}
	}
	return ""
}

func page(items []map[string]any) map[string]any {
	return map[string]any{"Items": items, "TotalRecordCount": len(items)}
}
func userDTO(u store.User, serverID string) map[string]any {
	return map[string]any{"Id": u.ID, "Name": u.Name, "ServerId": serverID, "HasPassword": true, "HasConfiguredPassword": true, "Policy": map[string]any{"IsAdministrator": u.IsAdmin, "IsDisabled": false, "IsHidden": false, "MaxParentalRating": zeroNil(u.MaxParentalRating)}}
}
func sessionDTO(s store.Session) map[string]any {
	return map[string]any{"Id": s.ID, "UserId": s.UserID, "UserName": s.UserName, "DeviceId": s.DeviceID, "DeviceName": s.DeviceName, "Client": s.Client, "ApplicationVersion": "", "IsActive": true, "SupportsMediaControl": false}
}
func jfType(t string) string {
	if t == "tvshows" {
		return "tvshows"
	}
	return "movies"
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, err error, code int) {
	if err == nil {
		err = errors.New(http.StatusText(code))
	}
	http.Error(w, err.Error(), code)
}
func log(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = os.Stdout.WriteString(r.Method + " " + r.URL.Path + "\n")
		next.ServeHTTP(w, r)
	})
}
func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
func firstInt(s string, d int) int {
	if n := atoi(s); n > 0 {
		return n
	}
	return d
}
func parseBool(s string) bool { return strings.EqualFold(s, "true") }
func (a API) allowed(u store.User, it store.Item) bool {
	if u.ID == "" || u.IsAdmin || !u.IsChild || u.MaxParentalRating == 0 {
		return true
	}
	score := ratingScore(it.OfficialRating)
	return score == 0 || score <= u.MaxParentalRating
}
func userFilter(q url.Values, name string) bool {
	return strings.EqualFold(q.Get(name), "true") || strings.Contains(q.Get("Filters"), name)
}
func (a API) personAllowed(u store.User, personID string) bool {
	if u.ID == "" || u.IsAdmin || !u.IsChild || u.MaxParentalRating == 0 {
		return true
	}
	items, _ := a.S.Items(store.ItemQuery{PersonIDs: personID, Type: "Movie,Series,Episode"})
	for _, it := range items {
		if a.allowed(u, it) {
			return true
		}
	}
	return len(items) == 0
}
func (a API) allowedCount(u store.User, items []store.Item) int {
	n := 0
	for _, it := range items {
		if a.allowed(u, it) {
			n++
		}
	}
	return n
}
func (a API) filterGenres(u store.User, items []store.Item) []string {
	seen := map[string]bool{}
	for _, it := range items {
		if !a.allowed(u, it) {
			continue
		}
		for _, g := range stringsJSON(it.GenresJSON) {
			if g != "" {
				seen[g] = true
			}
		}
	}
	return sortedKeys(seen)
}
func (a API) filterRatings(u store.User, items []store.Item) []string {
	seen := map[string]bool{}
	for _, it := range items {
		if a.allowed(u, it) && it.OfficialRating != "" {
			seen[it.OfficialRating] = true
		}
	}
	return sortedKeys(seen)
}
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func (a API) filterYears(u store.User, items []store.Item) []int {
	seen := map[int]bool{}
	for _, it := range items {
		if a.allowed(u, it) && it.ProductionYear != 0 {
			seen[it.ProductionYear] = true
		}
	}
	out := make([]int, 0, len(seen))
	for y := range seen {
		out = append(out, y)
	}
	sort.Ints(out)
	return out
}
func ratingScore(r string) int {
	switch strings.ToUpper(strings.TrimSpace(r)) {
	case "G", "TV-Y", "TV-Y7", "TV-G":
		return 1
	case "PG", "TV-PG":
		return 2
	case "PG-13", "TV-14":
		return 3
	case "R", "TV-MA":
		return 4
	case "NC-17":
		return 5
	default:
		return 0
	}
}
func parentalRatings() []map[string]any {
	return []map[string]any{
		{"Name": "G", "Value": 1},
		{"Name": "PG", "Value": 2},
		{"Name": "PG-13", "Value": 3},
		{"Name": "R", "Value": 4},
		{"Name": "NC-17", "Value": 5},
		{"Name": "TV-Y", "Value": 1},
		{"Name": "TV-Y7", "Value": 1},
		{"Name": "TV-G", "Value": 1},
		{"Name": "TV-PG", "Value": 2},
		{"Name": "TV-14", "Value": 3},
		{"Name": "TV-MA", "Value": 4},
	}
}
func zeroNil(n int) any {
	if n == 0 {
		return nil
	}
	return n
}
func zeroNil64(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
func zeroNilFloat(n float64) any {
	if n == 0 {
		return nil
	}
	return n
}
func emptyNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func providerIDs(s string) map[string]string {
	out := map[string]string{}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}
func stringsJSON(s string) []string {
	out := []string{}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}
func namedItems(names []string) []map[string]string {
	out := []map[string]string{}
	for _, n := range names {
		out = append(out, map[string]string{"Name": n, "Id": store.StableID("name", n)})
	}
	return out
}
func itemHint(it store.Item, primaryTag string) map[string]any {
	m := map[string]any{"ItemId": it.ID, "Id": it.ID, "Name": it.Name, "MatchedTerm": it.Name, "Type": it.Type, "MediaType": "Video", "ProductionYear": zeroNil(it.ProductionYear)}
	if it.IsFolder {
		m["MediaType"] = "Unknown"
	}
	if primaryTag != "" {
		m["PrimaryImageTag"] = primaryTag
	}
	return m
}
func personHint(p store.Person) map[string]any {
	m := map[string]any{"ItemId": p.ID, "Id": p.ID, "Name": p.Name, "MatchedTerm": p.Name, "Type": "Person", "MediaType": "Unknown"}
	if p.ProfileURL != "" {
		m["PrimaryImageTag"] = store.StableID(p.ProfileURL)
	}
	return m
}
func hintRank(name, term string) int {
	name, term = strings.ToLower(name), strings.ToLower(term)
	switch {
	case name == term:
		return 0
	case strings.HasPrefix(name, term):
		return 1
	default:
		return 2
	}
}
func (a API) personDTO(p store.Person, details bool) map[string]any {
	m := map[string]any{"Id": p.ID, "Name": p.Name, "Type": "Person", "IsFolder": true, "ImageTags": map[string]string{}, "ProviderIds": map[string]string{}, "PrimaryImageAspectRatio": 0.6666666666666666}
	if p.TMDBID != 0 {
		m["ProviderIds"].(map[string]string)["Tmdb"] = strconv.Itoa(p.TMDBID)
	}
	if p.IMDBID != "" {
		m["ProviderIds"].(map[string]string)["Imdb"] = p.IMDBID
	}
	if p.ProfileURL != "" {
		m["ImageTags"].(map[string]string)["Primary"] = store.StableID(p.ProfileURL)
	}
	if details {
		m["Overview"] = p.Biography
		m["PremiereDate"] = emptyNil(p.BirthDate)
		m["EndDate"] = emptyNil(p.DeathDate)
		m["CommunityRating"] = nil
		m["ProductionYear"] = year(p.BirthDate)
		m["SortName"] = p.Name
		m["MediaType"] = "Unknown"
		m["People"] = []any{}
		m["Studios"] = []any{}
		m["Genres"] = []any{}
		m["Taglines"] = []any{}
		m["ExternalUrls"] = personURLs(p)
		m["UserData"] = map[string]any{"PlaybackPositionTicks": 0, "PlayCount": 0, "IsFavorite": false, "Played": false, "Key": p.ID, "ItemId": p.ID}
		m["ProductionLocations"] = []string{}
		if p.PlaceOfBirth != "" {
			m["ProductionLocations"] = []string{p.PlaceOfBirth}
		}
		if p.KnownDepartment != "" {
			m["Taglines"] = []string{p.KnownDepartment}
		}
		items, _ := a.S.Items(store.ItemQuery{PersonIDs: p.ID, Type: "Movie,Series,Episode"})
		m["RecursiveItemCount"] = len(items)
	}
	return m
}
func people(s string) []map[string]any {
	out := []map[string]any{}
	for _, p := range peopleRaw(s) {
		id := store.StableID("person", p.Name)
		if p.TMDBID != 0 {
			id = store.StableID("person", strconv.Itoa(p.TMDBID))
		}
		m := map[string]any{"Name": p.Name, "Id": id, "Role": p.Role, "Type": p.Type, "ProviderIds": map[string]string{}}
		if p.TMDBID != 0 {
			m["ProviderIds"] = map[string]string{"Tmdb": strconv.Itoa(p.TMDBID)}
		}
		if p.ProfileURL != "" {
			m["PrimaryImageTag"] = store.StableID(p.ProfileURL)
		}
		out = append(out, m)
	}
	return out
}
func peopleRaw(s string) []struct {
	Name, Role, Type, ProfileURL string
	TMDBID                       int
} {
	out := []struct {
		Name, Role, Type, ProfileURL string
		TMDBID                       int
	}{}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}
func externalURLs(s string) []map[string]string {
	var in []struct{ Name, URL string }
	_ = json.Unmarshal([]byte(s), &in)
	out := []map[string]string{}
	for _, u := range in {
		out = append(out, map[string]string{"Name": u.Name, "Url": u.URL})
	}
	return out
}
func personURLs(p store.Person) []map[string]string {
	out := []map[string]string{}
	if p.TMDBID != 0 {
		out = append(out, map[string]string{"Name": "TheMovieDb", "Url": "https://www.themoviedb.org/person/" + strconv.Itoa(p.TMDBID)})
	}
	if p.IMDBID != "" {
		out = append(out, map[string]string{"Name": "IMDb", "Url": "https://www.imdb.com/name/" + p.IMDBID + "/"})
	}
	return out
}
func userData(p store.Playback, itemID string) map[string]any {
	return map[string]any{"PlaybackPositionTicks": p.PositionTicks, "PlayCount": p.PlayCount, "IsFavorite": p.Favorite, "Played": p.Played, "Likes": p.Likes, "Key": itemID, "ItemId": itemID}
}
func mediaSourceCount(it store.Item) int {
	if it.IsFolder {
		return 0
	}
	return 1
}
func stale(ts string, d time.Duration) bool {
	t, err := time.Parse(time.RFC3339, ts)
	return err != nil || time.Since(t) > d
}
func year(s string) any {
	if len(s) < 4 {
		return nil
	}
	n, _ := strconv.Atoi(s[:4])
	if n == 0 {
		return nil
	}
	return n
}
