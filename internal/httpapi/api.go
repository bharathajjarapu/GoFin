package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gofin/internal/config"
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
	m.HandleFunc("/Shows/NextUp", a.nextUp)
	m.HandleFunc("/Shows/", a.shows)
	m.HandleFunc("/UserItems/Resume", a.resume)
	m.HandleFunc("/UserItems/", a.userItems)
	m.HandleFunc("/Search/Hints", a.searchHints)
	m.HandleFunc("/Sessions", a.sessions)
	m.HandleFunc("/Sessions/", a.sessions)
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
	u, tok, err := a.S.Auth(in.Username, pass)
	if err != nil {
		fail(w, err, 401)
		return
	}
	write(w, map[string]any{"User": userDTO(u, a.C.Server.ID), "AccessToken": tok, "ServerId": a.C.Server.ID, "SessionInfo": map[string]any{"UserId": u.ID, "Id": store.StableID("session", u.ID, tok)}})
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
	items, err := a.S.Items(store.ItemQuery{ParentID: q.Get("ParentId"), Type: q.Get("IncludeItemTypes"), Search: q.Get("SearchTerm"), SortBy: q.Get("SortBy"), SortOrder: q.Get("SortOrder"), Recursive: parseBool(q.Get("Recursive")), Start: atoi(q.Get("StartIndex")), Limit: atoi(q.Get("Limit"))})
	if err != nil {
		fail(w, err, 500)
		return
	}
	out := []map[string]any{}
	for _, it := range items {
		out = append(out, a.itemDTO(it, u.ID))
	}
	write(w, page(out))
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
	write(w, a.itemDTO(it, u.ID))
}

func (a API) playbackInfo(w http.ResponseWriter, r *http.Request, id string) {
	it, err := a.S.Item(id)
	if err != nil {
		fail(w, err, 404)
		return
	}
	u, _ := a.userNoFail(r)
	dto := a.itemDTO(it, u.ID)
	write(w, map[string]any{"MediaSources": dto["MediaSources"], "PlaySessionId": store.StableID("play", id), "ErrorCode": ""})
}

func (a API) images(w http.ResponseWriter, r *http.Request, id string, parts []string) {
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
	http.NotFound(w, r)
}

func (a API) latest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	u, _ := a.userNoFail(r)
	items, err := a.S.Items(store.ItemQuery{ParentID: q.Get("ParentId"), Type: q.Get("IncludeItemTypes"), SortBy: "DateCreated", SortOrder: "Descending", Recursive: true, Limit: firstInt(q.Get("Limit"), 16)})
	if err != nil {
		fail(w, err, 500)
		return
	}
	out := []map[string]any{}
	for _, it := range items {
		if it.Type != "CollectionFolder" && !it.IsFolder {
			out = append(out, a.itemDTO(it, u.ID))
		}
	}
	write(w, out)
}

func (a API) resume(w http.ResponseWriter, r *http.Request) {
	u, _ := a.userNoFail(r)
	items, err := a.S.Resume(u.ID, firstInt(r.URL.Query().Get("Limit"), 16))
	if err != nil {
		fail(w, err, 500)
		return
	}
	out := []map[string]any{}
	for _, it := range items {
		out = append(out, a.itemDTO(it, u.ID))
	}
	write(w, page(out))
}

func (a API) shows(w http.ResponseWriter, r *http.Request) {
	u, _ := a.userNoFail(r)
	p := strings.TrimPrefix(r.URL.Path, "/Shows/")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	q := store.ItemQuery{ParentID: parts[0]}
	if parts[1] == "Seasons" {
		q.Type = "Season"
	}
	items, err := a.S.Items(q)
	if err != nil {
		fail(w, err, 500)
		return
	}
	if parts[1] == "Episodes" {
		var eps []store.Item
		for _, season := range items {
			children, err := a.S.Items(store.ItemQuery{ParentID: season.ID, Type: "Episode"})
			if err != nil {
				fail(w, err, 500)
				return
			}
			eps = append(eps, children...)
		}
		items = eps
	}
	out := []map[string]any{}
	for _, it := range items {
		out = append(out, a.itemDTO(it, u.ID))
	}
	write(w, page(out))
}

func (a API) nextUp(w http.ResponseWriter, r *http.Request) { write(w, page([]map[string]any{})) }
func (a API) counts(w http.ResponseWriter, r *http.Request) {
	movies, _ := a.S.Items(store.ItemQuery{Type: "Movie"})
	series, _ := a.S.Items(store.ItemQuery{Type: "Series"})
	episodes, _ := a.S.Items(store.ItemQuery{Type: "Episode"})
	write(w, map[string]int{"MovieCount": len(movies), "SeriesCount": len(series), "EpisodeCount": len(episodes)})
}
func (a API) filters(w http.ResponseWriter, r *http.Request) {
	genres, ratings, err := a.S.Filters()
	if err != nil {
		fail(w, err, 500)
		return
	}
	write(w, map[string]any{"Genres": genres, "Tags": []string{}, "OfficialRatings": ratings})
}
func (a API) searchHints(w http.ResponseWriter, r *http.Request) {
	write(w, map[string]any{"SearchHints": []any{}, "TotalRecordCount": 0})
}
func (a API) userItems(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }

func (a API) sessions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		write(w, []any{})
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
	http.ServeFile(w, r, it.Path)
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
	if strings.Contains(p, "/Items/") {
		id := p[strings.LastIndex(p, "/")+1:]
		it, err := a.S.Item(id)
		if err != nil {
			fail(w, err, 404)
			return
		}
		u, _ := a.userNoFail(r)
		write(w, a.itemDTO(it, u.ID))
		return
	}
	http.NotFound(w, r)
}

func (a API) itemDTO(it store.Item, userID string) map[string]any {
	imgs, _ := a.S.Images(it.ID)
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
	ud := a.userData(userID, it.ID)
	m := map[string]any{"Id": it.ID, "Name": it.Name, "ServerId": a.C.Server.ID, "Type": it.Type, "IsFolder": it.IsFolder, "ParentId": it.ParentID, "SortName": it.SortName, "DateCreated": it.DateCreated, "MediaType": mediaType, "ImageTags": imageTags, "BackdropImageTags": backdrops, "Overview": it.Overview, "ProductionYear": zeroNil(it.ProductionYear), "PremiereDate": emptyNil(it.PremiereDate), "IndexNumber": zeroNil(it.IndexNumber), "ParentIndexNumber": zeroNil(it.ParentIndexNumber), "ProviderIds": providerIDs(it.ProviderIDsJSON), "Genres": genres, "GenreItems": namedItems(genres), "Studios": namedItems(stringsJSON(it.StudiosJSON)), "People": people(it.PeopleJSON), "CommunityRating": zeroNilFloat(it.CommunityRating), "OfficialRating": emptyNil(it.OfficialRating), "RunTimeTicks": zeroNil64(it.RuntimeTicks), "Taglines": stringsJSON(it.TaglinesJSON), "ExternalUrls": externalURLs(it.ExternalURLsJSON), "MediaSourceCount": mediaSourceCount(it), "CanDownload": !it.IsFolder, "Container": emptyNil(it.Container), "Path": emptyNil(it.Path), "PrimaryImageAspectRatio": 0.6666666666666666, "UserData": ud}
	if it.Type == "CollectionFolder" {
		m["CollectionType"] = "movies"
	}
	if !it.IsFolder {
		m["MediaSources"] = []map[string]any{a.mediaSource(it)}
		m["MediaStreams"] = []any{}
	}
	return m
}

func (a API) mediaSource(it store.Item) map[string]any {
	return map[string]any{"Id": store.StableID("source", it.ID), "Path": it.Path, "Protocol": "File", "Type": "Default", "Container": it.Container, "Size": it.Size, "Name": filepath.Base(it.Path), "RunTimeTicks": zeroNil64(it.RuntimeTicks), "IsRemote": false, "SupportsDirectPlay": true, "SupportsDirectStream": true, "SupportsTranscoding": false, "MediaStreams": []any{}, "DirectStreamUrl": "/Videos/" + it.ID + "/stream?Static=true&Container=" + it.Container}
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

func page(items []map[string]any) map[string]any {
	return map[string]any{"Items": items, "TotalRecordCount": len(items)}
}
func userDTO(u store.User, serverID string) map[string]any {
	return map[string]any{"Id": u.ID, "Name": u.Name, "ServerId": serverID, "HasPassword": true, "HasConfiguredPassword": true, "Policy": map[string]any{"IsAdministrator": u.IsAdmin}}
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
func people(s string) []map[string]any {
	var in []struct{ Name, Role, Type string }
	_ = json.Unmarshal([]byte(s), &in)
	out := []map[string]any{}
	for _, p := range in {
		out = append(out, map[string]any{"Name": p.Name, "Id": store.StableID("person", p.Name), "Role": p.Role, "Type": p.Type})
	}
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
func (a API) userData(userID, itemID string) map[string]any {
	p := store.Playback{}
	if userID != "" {
		p = a.S.Playback(userID, itemID)
	}
	return map[string]any{"PlaybackPositionTicks": p.PositionTicks, "PlayCount": p.PlayCount, "IsFavorite": false, "Played": p.Played, "Key": itemID, "ItemId": itemID}
}
func mediaSourceCount(it store.Item) int {
	if it.IsFolder {
		return 0
	}
	return 1
}
