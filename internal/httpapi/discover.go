package httpapi

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"gofin/internal/store"
)

const (
	// similarLimit is how many suggestions a client is offered by default.
	similarLimit = 12
	// similarYears is how far apart two releases can be and still count as
	// contemporaries.
	similarYears = 5
	// Weights for what two items share. A person in common is a stronger
	// signal than a genre in common, which most of a library shares anyway.
	genreWeight  = 2
	personWeight = 3
)

// similar suggests other items like this one, scoring them on the metadata
// already stored: shared genres, shared cast and crew, and nearness in year.
// It is deliberately a pass over the local library rather than an online
// lookup, so it needs no API key and works for a library TMDB has never heard
// of.
func (a API) similar(w http.ResponseWriter, r *http.Request, id string) {
	u, _ := a.userNoFail(r)
	it, err := a.S.Item(id)
	if err != nil {
		fail(w, err, http.StatusNotFound)
		return
	}
	candidates, err := a.S.Items(store.ItemQuery{Type: it.Type})
	if err != nil {
		fail(w, err, http.StatusInternalServerError)
		return
	}
	genres := nameSet(stringsJSON(it.GenresJSON))
	people := map[string]bool{}
	for _, p := range peopleRaw(it.PeopleJSON) {
		people[p.Name] = true
	}

	type ranked struct {
		item  store.Item
		score int
	}
	var matches []ranked
	for _, candidate := range candidates {
		if candidate.ID == it.ID || !a.allowed(u, candidate) {
			continue
		}
		if score := similarScore(it, candidate, genres, people); score > 0 {
			matches = append(matches, ranked{candidate, score})
		}
	}
	// A stable sort keeps equally scored items in library order rather than
	// shuffling them between requests.
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })

	limit := firstInt(r.URL.Query().Get("Limit"), similarLimit)
	if len(matches) > limit {
		matches = matches[:limit]
	}
	items := make([]store.Item, 0, len(matches))
	for _, match := range matches {
		items = append(items, match.item)
	}
	write(w, page(a.itemDTOs(items, u.ID), len(items)))
}

// similarScore weighs what a candidate shares with the item being viewed.
func similarScore(it, candidate store.Item, genres, people map[string]bool) int {
	score := 0
	for _, genre := range stringsJSON(candidate.GenresJSON) {
		if genres[genre] {
			score += genreWeight
		}
	}
	for _, p := range peopleRaw(candidate.PeopleJSON) {
		if people[p.Name] {
			score += personWeight
		}
	}
	if it.ProductionYear != 0 && candidate.ProductionYear != 0 {
		if gap := it.ProductionYear - candidate.ProductionYear; gap <= similarYears && -gap <= similarYears {
			score++
		}
	}
	return score
}

func nameSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

// refresh re-reads one item and its metadata. This used to answer 204 without
// doing anything, so a client's "refresh metadata" appeared to work and then
// changed nothing.
func (a API) refresh(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.Refresh == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := a.Refresh(id); err != nil {
		fail(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a API) genres(w http.ResponseWriter, r *http.Request) {
	a.browseAxis(w, r, "/Genres", "Genre", store.ItemQuery{Type: "Movie,Series"}, a.filterGenres)
}

func (a API) studios(w http.ResponseWriter, r *http.Request) {
	a.browseAxis(w, r, "/Studios", "Studio", store.ItemQuery{Type: "Movie,Series"}, a.filterStudios)
}

// musicGenres is the music half of the same browse axis. Jellyfin keeps the
// two separate so a music client never offers film genres.
func (a API) musicGenres(w http.ResponseWriter, r *http.Request) {
	a.browseAxis(w, r, "/MusicGenres", "MusicGenre", store.ItemQuery{Type: "MusicAlbum,Audio"}, a.filterGenres)
}

// browseAxis answers a browse-by endpoint: the distinct names a field takes
// across a set of items, or one of those names on its own so that tapping it
// opens something instead of 404ing.
func (a API) browseAxis(w http.ResponseWriter, r *http.Request, prefix, typ string, query store.ItemQuery, names func(store.User, []store.Item) []string) {
	u, _ := a.userNoFail(r)
	name, err := url.PathUnescape(strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	items, err := a.S.Items(query)
	if err != nil {
		fail(w, err, http.StatusInternalServerError)
		return
	}
	values := names(u, items)
	if name != "" {
		for _, value := range values {
			if strings.EqualFold(value, name) {
				write(w, a.namedFolderDTO(value, typ))
				return
			}
		}
		http.NotFound(w, r)
		return
	}
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		out = append(out, a.namedFolderDTO(value, typ))
	}
	write(w, page(out, len(out)))
}

// namedFolderDTO renders a genre or studio as the folder-shaped item a client
// browses into.
func (a API) namedFolderDTO(name, typ string) map[string]any {
	return map[string]any{"Name": name, "Id": store.StableID("name", name), "Type": typ,
		"ServerId": a.C.Server.ID, "IsFolder": true, "ImageTags": map[string]string{}}
}
