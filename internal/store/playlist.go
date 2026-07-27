package store

import (
	"database/sql"
	"slices"
	"strconv"
	"time"
)

// PlaylistCollection is the collection type of the folder playlists live in.
// Nothing on disk backs it, which is why scans skip it and library sweeps
// leave it alone.
const PlaylistCollection = "playlists"

// PlaylistEntry is one position in a playlist. Its id is distinct from the
// item's so that the same track can appear twice and still be moved or removed
// without ambiguity, which is the model Jellyfin clients expect.
type PlaylistEntry struct {
	EntryID, ItemID string
	Item            Item
}

// PlaylistLibraryID is fixed so the folder is found again after a restart.
func PlaylistLibraryID() string { return StableID("library", PlaylistCollection) }

// ensurePlaylistLibrary creates the folder that holds playlists. Playlists are
// ordinary item rows, so browsing, favourites, images and parental filtering
// all apply to them without any further work.
func (s *Store) ensurePlaylistLibrary() (string, error) {
	id := PlaylistLibraryID()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.DB.Exec(`INSERT INTO libraries(id,name,collection_type,path,created_at,updated_at)
		VALUES(?,?,?,?,?,?) ON CONFLICT(path) DO NOTHING`,
		id, "Playlists", PlaylistCollection, PlaylistCollection, now, now); err != nil {
		return "", err
	}
	_, err := s.DB.Exec(`INSERT INTO items(id,library_id,type,name,sort_name,is_folder,date_created)
		VALUES(?,?,?,?,?,1,?) ON CONFLICT(id) DO NOTHING`,
		id, id, "CollectionFolder", "Playlists", "playlists", now)
	return id, err
}

// CreatePlaylist adds an empty playlist. The id mixes in the creation time so
// that two playlists may share a name.
func (s *Store) CreatePlaylist(name string) (Item, error) {
	libraryID, err := s.ensurePlaylistLibrary()
	if err != nil {
		return Item{}, err
	}
	it := Item{
		ID:        StableID("playlist", name, time.Now().UTC().Format(time.RFC3339Nano)),
		LibraryID: libraryID, ParentID: libraryID,
		Type: "Playlist", Name: name, IsFolder: true,
	}
	return it, s.UpsertItem(it)
}

// DeletePlaylist removes a playlist and, by cascade, its entries. It refuses
// anything that is not a playlist, so the same route cannot delete media.
func (s *Store) DeletePlaylist(id string) error {
	res, err := s.DB.Exec(`DELETE FROM items WHERE id=? AND type='Playlist'`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

// PlaylistEntries lists a playlist in running order, dropping entries whose
// item has since left the library.
func (s *Store) PlaylistEntries(playlistID string) ([]PlaylistEntry, error) {
	rows, err := s.DB.Query(`SELECT entry_id,item_id FROM playlist_entries WHERE playlist_id=? ORDER BY position`, playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []PlaylistEntry
	var ids []string
	for rows.Next() {
		var entry PlaylistEntry
		if err := rows.Scan(&entry.EntryID, &entry.ItemID); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
		ids = append(ids, entry.ItemID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items, err := s.ItemsByIDs(ids)
	if err != nil {
		return nil, err
	}
	out := entries[:0]
	for _, entry := range entries {
		if item, ok := items[entry.ItemID]; ok {
			entry.Item = item
			out = append(out, entry)
		}
	}
	return out, nil
}

// AddToPlaylist appends items in the order given, ignoring ids that name no
// playable item so that one bad id does not reject the whole request.
func (s *Store) AddToPlaylist(playlistID string, itemIDs []string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var next int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(position)+1,0) FROM playlist_entries WHERE playlist_id=?`, playlistID).Scan(&next); err != nil {
		return err
	}
	for _, id := range itemIDs {
		var playable int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM items WHERE id=? AND is_folder=0`, id).Scan(&playable); err != nil {
			return err
		}
		if playable == 0 {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO playlist_entries(entry_id,playlist_id,item_id,position) VALUES(?,?,?,?)`,
			StableID("entry", playlistID, id, strconv.Itoa(next)), playlistID, id, next); err != nil {
			return err
		}
		next++
	}
	return tx.Commit()
}

// RemoveFromPlaylist drops the named entries.
func (s *Store) RemoveFromPlaylist(playlistID string, entryIDs []string) error {
	if len(entryIDs) == 0 {
		return nil
	}
	args := append([]any{playlistID}, anys(entryIDs)...)
	_, err := s.DB.Exec(`DELETE FROM playlist_entries WHERE playlist_id=? AND entry_id IN (`+marksN(len(entryIDs))+`)`, args...)
	return err
}

// MovePlaylistEntry puts an entry at index and renumbers the rest. Rewriting
// every position keeps them dense and gap-free however many moves have
// happened, which is cheap for the size a playlist actually reaches.
func (s *Store) MovePlaylistEntry(playlistID, entryID string, index int) error {
	order, err := s.playlistOrder(playlistID)
	if err != nil {
		return err
	}
	from := slices.Index(order, entryID)
	if from < 0 {
		return sql.ErrNoRows
	}
	order = slices.Delete(order, from, from+1)
	order = slices.Insert(order, min(max(index, 0), len(order)), entryID)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for position, id := range order {
		if _, err := tx.Exec(`UPDATE playlist_entries SET position=? WHERE entry_id=?`, position, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// playlistOrder returns the entry ids of a playlist in running order.
func (s *Store) playlistOrder(playlistID string) ([]string, error) {
	rows, err := s.DB.Query(`SELECT entry_id FROM playlist_entries WHERE playlist_id=? ORDER BY position`, playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// PlaylistCounts reports how many entries each playlist holds. Entries hang off
// a side table rather than parent_id, so the folder statistics query cannot see
// them.
func (s *Store) PlaylistCounts(ids []string) (map[string]int, error) {
	out := map[string]int{}
	for _, ids := range chunks(ids, 900) {
		rows, err := s.DB.Query(`SELECT playlist_id,COUNT(*) FROM playlist_entries WHERE playlist_id IN (`+marksN(len(ids))+`) GROUP BY playlist_id`, anys(ids)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = n
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
