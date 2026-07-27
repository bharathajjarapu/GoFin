package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"gofin/internal/store"
)

// playlists routes the playlist endpoints. Playlists are stored as ordinary
// items, so only membership and ordering need routes of their own; browsing
// them goes through /Items like anything else.
//
//	POST   /Playlists                               create
//	GET    /Playlists/{id}/Items                    list in running order
//	POST   /Playlists/{id}/Items?Ids=…              append
//	DELETE /Playlists/{id}/Items?EntryIds=…         remove
//	POST   /Playlists/{id}/Items/{entryId}/Move/{n} reorder
func (a API) playlists(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(strings.TrimPrefix(r.URL.Path, "/Playlists"))
	if len(parts) == 0 {
		a.createPlaylist(w, r)
		return
	}
	playlist, err := a.S.Item(parts[0])
	if err != nil || playlist.Type != "Playlist" {
		http.NotFound(w, r)
		return
	}
	if len(parts) < 2 || parts[1] != "Items" {
		http.NotFound(w, r)
		return
	}
	if len(parts) >= 4 && parts[3] == "Move" {
		a.movePlaylistEntry(w, r, playlist.ID, parts[2], parts[4:])
		return
	}
	switch r.Method {
	case http.MethodGet:
		a.playlistItems(w, r, playlist.ID)
	case http.MethodPost:
		a.addPlaylistItems(w, r, playlist.ID)
	case http.MethodDelete:
		a.removePlaylistItems(w, r, playlist.ID)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a API) createPlaylist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Name string
		Ids  []string
	}
	// Clients send the name in the body, but some put it in the query instead.
	_ = decodeJSON(w, r, &in)
	name := firstNonEmpty(in.Name, r.URL.Query().Get("Name"), "New Playlist")
	playlist, err := a.S.CreatePlaylist(name)
	if err != nil {
		fail(w, err, http.StatusInternalServerError)
		return
	}
	ids := in.Ids
	if len(ids) == 0 {
		ids = splitList(r.URL.Query().Get("Ids"))
	}
	if err := a.S.AddToPlaylist(playlist.ID, ids); err != nil {
		fail(w, err, http.StatusInternalServerError)
		return
	}
	write(w, map[string]any{"Id": playlist.ID})
}

func (a API) playlistItems(w http.ResponseWriter, r *http.Request, playlistID string) {
	u, _ := a.userNoFail(r)
	entries, err := a.S.PlaylistEntries(playlistID)
	if err != nil {
		fail(w, err, http.StatusInternalServerError)
		return
	}
	items := make([]store.Item, 0, len(entries))
	entryIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		// A shared playlist may name media a restricted account must not see.
		if !a.allowed(u, entry.Item) {
			continue
		}
		items = append(items, entry.Item)
		entryIDs = append(entryIDs, entry.EntryID)
	}
	total := len(items)
	start, limit := atoi(r.URL.Query().Get("StartIndex")), queryLimit(r.URL.Query().Get("Limit"))
	items = pagedItems(items, start, limit)
	entryIDs = pagedStrings(entryIDs, start, limit)
	dtos := a.itemDTOs(items, u.ID)
	for i := range dtos {
		// PlaylistItemId is how a client names the position it wants to move
		// or remove, as distinct from the item sitting there.
		dtos[i]["PlaylistItemId"] = entryIDs[i]
	}
	write(w, page(dtos, total))
}

func (a API) addPlaylistItems(w http.ResponseWriter, r *http.Request, playlistID string) {
	if err := a.S.AddToPlaylist(playlistID, splitList(r.URL.Query().Get("Ids"))); err != nil {
		fail(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a API) removePlaylistItems(w http.ResponseWriter, r *http.Request, playlistID string) {
	q := r.URL.Query()
	if err := a.S.RemoveFromPlaylist(playlistID, splitList(firstNonEmpty(q.Get("EntryIds"), q.Get("Ids")))); err != nil {
		fail(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a API) movePlaylistEntry(w http.ResponseWriter, r *http.Request, playlistID, entryID string, rest []string) {
	if r.Method != http.MethodPost || len(rest) == 0 {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	index, err := strconv.Atoi(rest[0])
	if err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	if err := a.S.MovePlaylistEntry(playlistID, entryID, index); err != nil {
		fail(w, err, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pathParts splits a URL remainder into its non-empty segments.
func pathParts(p string) []string {
	if p = strings.Trim(p, "/"); p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// splitList reads a comma-separated query parameter.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// pagedStrings applies the same window pagedItems applies, so an entry id
// stays alongside the item it names.
func pagedStrings(values []string, start, limit int) []string {
	if start >= len(values) {
		return nil
	}
	if start > 0 {
		values = values[start:]
	}
	if limit > 0 && len(values) > limit {
		return values[:limit]
	}
	return values
}
