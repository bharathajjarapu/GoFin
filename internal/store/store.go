package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// ErrInvalidCredentials is returned for every rejected login attempt.
var ErrInvalidCredentials = errors.New("invalid credentials")

// This is a bcrypt hash for a fixed, non-secret value. Comparing it when a
// user is absent keeps failed-login work close to the wrong-password path.
const dummyPasswordHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

type Store struct{ DB *sql.DB }

type User struct {
	ID, Name          string
	IsAdmin, IsChild  bool
	MaxParentalRating int
}

type Session struct {
	ID, UserID, UserName, DeviceID, DeviceName, Client string
}

type Library struct{ ID, Name, Type, Path string }

type Item struct {
	ID, LibraryID, ParentID, Type, Name, SortName, Path, RelativePath   string
	DateCreated, PremiereDate, Overview, ProviderIDsJSON                string
	GenresJSON, StudiosJSON, PeopleJSON, TaglinesJSON, ExternalURLsJSON string
	OfficialRating, Container, LastSeenScan                             string
	Album, AlbumArtist, ArtistsJSON                                     string
	IsFolder, HasLyrics                                                 bool
	ProductionYear, IndexNumber, ParentIndexNumber                      int
	Size, RuntimeTicks, MTimeUnix                                       int64
	CommunityRating                                                     float64
}

type Image struct {
	ItemID, Type, Path, Tag, Mime string
	Index                         int
}

type Person struct {
	ID, Name, Role, Character, ProfileURL, IMDBID                  string
	Biography, BirthDate, DeathDate, PlaceOfBirth, KnownDepartment string
	UpdatedAt                                                      string
	TMDBID, SortOrder                                              int
}

type PersonQuery struct {
	Search, StartsWith, AppearsInItemID, Types, ExcludeTypes string
	Start, Limit                                             int
}

type ItemQuery struct {
	ParentID, Type, Search, SortBy, SortOrder, PersonIDs, Genres, OfficialRatings, Years, NameStartsWith string
	UserID, Name, IDs, ArtistIDs, ExcludeTypes                                                           string
	Recursive                                                                                            bool
	Favorite, Played, Unplayed                                                                           bool
	Start, Limit                                                                                         int
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	return s, s.Migrate()
}

func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Migrate() error {
	if _, err := s.DB.Exec(schema); err != nil {
		return err
	}
	for _, c := range []string{
		"genres_json TEXT", "studios_json TEXT", "people_json TEXT", "taglines_json TEXT", "external_urls_json TEXT", "community_rating REAL", "official_rating TEXT", "last_seen_scan TEXT",
		"album TEXT", "album_artist TEXT", "artists_json TEXT", "has_lyrics INTEGER NOT NULL DEFAULT 0",
	} {
		if err := s.addColumn("items", c); err != nil {
			return err
		}
	}
	for _, c := range []string{
		"imdb_id TEXT", "biography TEXT", "birth_date TEXT", "death_date TEXT", "place_of_birth TEXT", "known_for_department TEXT",
	} {
		if err := s.addColumn("people", c); err != nil {
			return err
		}
	}
	for _, c := range []string{"is_child INTEGER NOT NULL DEFAULT 0", "max_parental_rating INTEGER NOT NULL DEFAULT 0"} {
		if err := s.addColumn("users", c); err != nil {
			return err
		}
	}
	for _, c := range []string{"is_favorite INTEGER NOT NULL DEFAULT 0", "likes INTEGER"} {
		if err := s.addColumn("playback_state", c); err != nil {
			return err
		}
	}
	return s.migratePlaybackState()
}

// Keep watch history when a scan temporarily removes an item, such as while a
// media mount is unavailable. Stable item IDs reconnect these rows on rescan.
func (s *Store) migratePlaybackState() error {
	rows, err := s.DB.Query(`PRAGMA foreign_key_list(playback_state)`)
	if err != nil {
		return err
	}
	hasItemFK := false
	for rows.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return err
		}
		if table != "items" {
			continue
		}
		hasItemFK = true
		break
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil || !hasItemFK {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`CREATE TABLE playback_state_new (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, item_id TEXT NOT NULL, played INTEGER NOT NULL DEFAULT 0, play_count INTEGER NOT NULL DEFAULT 0, playback_position_ticks INTEGER NOT NULL DEFAULT 0, is_favorite INTEGER NOT NULL DEFAULT 0, likes INTEGER, last_played_at TEXT, updated_at TEXT NOT NULL, PRIMARY KEY(user_id, item_id))`,
		`INSERT INTO playback_state_new SELECT user_id,item_id,played,play_count,playback_position_ticks,is_favorite,likes,last_played_at,updated_at FROM playback_state`,
		`DROP TABLE playback_state`,
		`ALTER TABLE playback_state_new RENAME TO playback_state`,
		`CREATE INDEX idx_playback_favorite ON playback_state(user_id, is_favorite)`,
		`CREATE INDEX idx_playback_played ON playback_state(user_id, played)`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SaveLibraries(libs []Library) error {
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The playlists folder is never configured and never scanned, so it must
	// survive this sweep. Dropping it would cascade every playlist away on the
	// next restart.
	if len(libs) == 0 {
		if _, err := tx.Exec(`DELETE FROM libraries WHERE collection_type<>?`, PlaylistCollection); err != nil {
			return err
		}
	} else {
		args := make([]any, 0, len(libs)+1)
		for _, l := range libs {
			args = append(args, l.ID)
		}
		args = append(args, PlaylistCollection)
		if _, err := tx.Exec(`DELETE FROM libraries WHERE id NOT IN (`+strings.TrimRight(strings.Repeat("?,", len(libs)), ",")+`) AND collection_type<>?`, args...); err != nil {
			return err
		}
	}
	for _, l := range libs {
		if l.ID == "" {
			l.ID = StableID("library", l.Path)
		}
		_, err = tx.Exec(`INSERT INTO libraries(id,name,collection_type,path,created_at,updated_at)
			VALUES(?,?,?,?,?,?)
			ON CONFLICT(path) DO UPDATE SET name=excluded.name, collection_type=excluded.collection_type, updated_at=excluded.updated_at`, l.ID, l.Name, l.Type, l.Path, now, now)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO items(id,library_id,type,name,sort_name,path,is_folder,date_created)
			VALUES(?,?,?,?,?,?,1,?)
			ON CONFLICT(id) DO UPDATE SET name=excluded.name, sort_name=excluded.sort_name, path=excluded.path`, l.ID, l.ID, "CollectionFolder", l.Name, strings.ToLower(l.Name), l.Path, now)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Libraries() ([]Library, error) {
	rows, err := s.DB.Query(`SELECT id,name,collection_type,path FROM libraries ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Library
	for rows.Next() {
		var l Library
		if err := rows.Scan(&l.ID, &l.Name, &l.Type, &l.Path); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) UpsertItem(it Item) error {
	now := time.Now().UTC().Format(time.RFC3339)
	folder, lyrics := 0, 0
	if it.IsFolder {
		folder = 1
	}
	if it.HasLyrics {
		lyrics = 1
	}
	if it.SortName == "" {
		it.SortName = strings.ToLower(it.Name)
	}
	_, err := s.DB.Exec(`INSERT INTO items(id,library_id,parent_id,type,name,sort_name,path,relative_path,is_folder,production_year,premiere_date,overview,index_number,parent_index_number,date_created,provider_ids_json,genres_json,studios_json,people_json,taglines_json,external_urls_json,community_rating,official_rating,runtime_ticks,last_seen_scan,album,album_artist,artists_json,has_lyrics)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET parent_id=excluded.parent_id,type=excluded.type,name=excluded.name,sort_name=excluded.sort_name,path=excluded.path,relative_path=excluded.relative_path,is_folder=excluded.is_folder,production_year=COALESCE(excluded.production_year,items.production_year),premiere_date=COALESCE(excluded.premiere_date,items.premiere_date),overview=COALESCE(excluded.overview,items.overview),index_number=COALESCE(excluded.index_number,items.index_number),parent_index_number=COALESCE(excluded.parent_index_number,items.parent_index_number),provider_ids_json=COALESCE(excluded.provider_ids_json,items.provider_ids_json),genres_json=COALESCE(excluded.genres_json,items.genres_json),studios_json=COALESCE(excluded.studios_json,items.studios_json),people_json=COALESCE(excluded.people_json,items.people_json),taglines_json=COALESCE(excluded.taglines_json,items.taglines_json),external_urls_json=COALESCE(excluded.external_urls_json,items.external_urls_json),community_rating=COALESCE(excluded.community_rating,items.community_rating),official_rating=COALESCE(excluded.official_rating,items.official_rating),runtime_ticks=COALESCE(excluded.runtime_ticks,items.runtime_ticks),last_seen_scan=COALESCE(excluded.last_seen_scan,items.last_seen_scan),album=COALESCE(excluded.album,items.album),album_artist=COALESCE(excluded.album_artist,items.album_artist),artists_json=COALESCE(excluded.artists_json,items.artists_json),has_lyrics=excluded.has_lyrics`,
		it.ID, it.LibraryID, nullEmpty(it.ParentID), it.Type, it.Name, it.SortName, nullEmpty(it.Path), nullEmpty(it.RelativePath), folder, nullZero(it.ProductionYear), nullEmpty(it.PremiereDate), nullEmpty(it.Overview), nullZero(it.IndexNumber), nullZero(it.ParentIndexNumber), now, nullEmpty(it.ProviderIDsJSON), nullEmpty(it.GenresJSON), nullEmpty(it.StudiosJSON), nullEmpty(it.PeopleJSON), nullEmpty(it.TaglinesJSON), nullEmpty(it.ExternalURLsJSON), nullFloat(it.CommunityRating), nullEmpty(it.OfficialRating), nullInt64(it.RuntimeTicks), nullEmpty(it.LastSeenScan), nullEmpty(it.Album), nullEmpty(it.AlbumArtist), nullEmpty(it.ArtistsJSON), lyrics)
	if err != nil {
		return err
	}
	if !it.IsFolder && it.Path != "" {
		_, err = s.DB.Exec(`INSERT INTO media_sources(id,item_id,path,container,size_bytes,mtime_unix,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET path=excluded.path, container=excluded.container, size_bytes=excluded.size_bytes, mtime_unix=excluded.mtime_unix, updated_at=excluded.updated_at`, StableID("source", it.ID), it.ID, it.Path, it.Container, it.Size, it.MTimeUnix, now, now)
	}
	return err
}

func (s *Store) TouchItem(id, scanID string) error {
	_, err := s.DB.Exec(`UPDATE items SET last_seen_scan=? WHERE id=?`, scanID, id)
	return err
}

func (s *Store) Unchanged(id string, size, mtime int64) bool {
	var gotSize, gotMTime int64
	err := s.DB.QueryRow(`SELECT size_bytes,mtime_unix FROM media_sources WHERE item_id=?`, id).Scan(&gotSize, &gotMTime)
	return err == nil && gotSize == size && gotMTime == mtime
}

func (s *Store) CleanupLibrary(libraryID, scanID string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM items WHERE library_id=? AND is_folder=0 AND COALESCE(last_seen_scan,'')<>?`, libraryID, scanID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM items WHERE library_id=? AND type='Season' AND id NOT IN (SELECT parent_id FROM items WHERE type='Episode' AND parent_id IS NOT NULL)`, libraryID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM items WHERE library_id=? AND type='Series' AND id NOT IN (SELECT parent_id FROM items WHERE type='Season' AND parent_id IS NOT NULL)`, libraryID); err != nil {
		return err
	}
	// Albums and artists are folders, so the sweep above leaves them behind
	// once their tracks are gone. Drop them the same way empty seasons and
	// series are dropped, which also lets a scan reclaim their cached art.
	if _, err := tx.Exec(`DELETE FROM items WHERE library_id=? AND type='MusicAlbum' AND id NOT IN (SELECT parent_id FROM items WHERE type='Audio' AND parent_id IS NOT NULL)`, libraryID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM items WHERE library_id=? AND type='MusicArtist' AND id NOT IN (SELECT parent_id FROM items WHERE type='MusicAlbum' AND parent_id IS NOT NULL)`, libraryID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM people WHERE id NOT IN (SELECT person_id FROM item_people)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpsertImage(img Image) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if img.Tag == "" {
		img.Tag = StableID("image", img.Path)
	}
	_, err := s.DB.Exec(`INSERT INTO images(id,item_id,image_type,image_index,path,tag,mime_type,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(item_id,image_type,image_index) DO UPDATE SET path=excluded.path,tag=excluded.tag,mime_type=excluded.mime_type,updated_at=excluded.updated_at`, StableID("image", img.ItemID, img.Type, strconv.Itoa(img.Index)), img.ItemID, img.Type, img.Index, img.Path, img.Tag, img.Mime, now, now)
	return err
}

func (s *Store) SavePeople(itemID string, people []Person) error {
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM item_people WHERE item_id=?`, itemID); err != nil {
		return err
	}
	for i, p := range people {
		if p.Name == "" {
			continue
		}
		if p.ID == "" {
			if p.TMDBID != 0 {
				p.ID = StableID("person", strconv.Itoa(p.TMDBID))
			} else {
				p.ID = StableID("person", p.Name)
			}
		}
		_, err = tx.Exec(`INSERT INTO people(id,tmdb_id,imdb_id,name,profile_url,updated_at)
			VALUES(?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET tmdb_id=COALESCE(excluded.tmdb_id,people.tmdb_id), imdb_id=COALESCE(excluded.imdb_id,people.imdb_id), name=excluded.name, profile_url=COALESCE(excluded.profile_url,people.profile_url), updated_at=excluded.updated_at`,
			p.ID, nullZero(p.TMDBID), nullEmpty(p.IMDBID), p.Name, nullEmpty(p.ProfileURL), now)
		if err != nil {
			return err
		}
		if p.SortOrder == 0 {
			p.SortOrder = i
		}
		_, err = tx.Exec(`INSERT INTO item_people(item_id,person_id,role,character,sort_order)
			VALUES(?,?,?,?,?)
			ON CONFLICT(item_id,person_id,role,character) DO UPDATE SET sort_order=excluded.sort_order`,
			itemID, p.ID, p.Role, p.Character, p.SortOrder)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) PersonImageByID(id string) (string, error) {
	var out string
	err := s.DB.QueryRow(`SELECT COALESCE(profile_url,'') FROM people WHERE id=? AND COALESCE(profile_url,'')<>'' LIMIT 1`, id).Scan(&out)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return out, err
}

func (s *Store) People(q PersonQuery) ([]Person, error) {
	sqlq := selectPerson + ` FROM people p`
	var args []any
	needLinks := q.AppearsInItemID != "" || q.Types != "" || q.ExcludeTypes != ""
	if needLinks {
		sqlq += ` JOIN item_people ip ON ip.person_id=p.id`
	}
	sqlq += ` WHERE 1=1`
	if q.Search != "" {
		sqlq += ` AND p.name LIKE ? COLLATE NOCASE`
		args = append(args, "%"+q.Search+"%")
	}
	if q.StartsWith != "" {
		sqlq += ` AND p.name LIKE ? COLLATE NOCASE`
		args = append(args, q.StartsWith+"%")
	}
	if q.AppearsInItemID != "" {
		sqlq += ` AND ip.item_id=?`
		args = append(args, q.AppearsInItemID)
	}
	if q.Types != "" {
		sqlq += ` AND ip.role IN (` + marks(q.Types) + `)`
		for _, t := range split(q.Types) {
			args = append(args, t)
		}
	}
	if q.ExcludeTypes != "" {
		sqlq += ` AND ip.role NOT IN (` + marks(q.ExcludeTypes) + `)`
		for _, t := range split(q.ExcludeTypes) {
			args = append(args, t)
		}
	}
	sqlq += ` GROUP BY p.id ORDER BY p.name COLLATE NOCASE`
	switch {
	case q.Limit > 0:
		sqlq += ` LIMIT ?`
		args = append(args, q.Limit)
	case q.Start > 0:
		// SQLite rejects OFFSET on its own, and -1 is its "no limit" limit.
		sqlq += ` LIMIT -1`
	}
	if q.Start > 0 {
		sqlq += ` OFFSET ?`
		args = append(args, q.Start)
	}
	rows, err := s.DB.Query(sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPeople(rows)
}

func (s *Store) PersonByName(name string) (Person, error) {
	rows, err := s.DB.Query(selectPerson+` FROM people p WHERE p.name=? COLLATE NOCASE LIMIT 1`, name)
	if err != nil {
		return Person{}, err
	}
	defer rows.Close()
	people, err := scanPeople(rows)
	if err != nil {
		return Person{}, err
	}
	if len(people) == 0 {
		return Person{}, sql.ErrNoRows
	}
	return people[0], nil
}

func (s *Store) UpdatePersonDetails(p Person) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(`UPDATE people SET imdb_id=COALESCE(?,imdb_id), biography=COALESCE(?,biography), birth_date=COALESCE(?,birth_date), death_date=COALESCE(?,death_date), place_of_birth=COALESCE(?,place_of_birth), known_for_department=COALESCE(?,known_for_department), updated_at=? WHERE id=?`,
		nullEmpty(p.IMDBID), nullEmpty(p.Biography), nullEmpty(p.BirthDate), nullEmpty(p.DeathDate), nullEmpty(p.PlaceOfBirth), nullEmpty(p.KnownDepartment), now, p.ID)
	return err
}

func (s *Store) Images(itemID string) ([]Image, error) {
	rows, err := s.DB.Query(`SELECT item_id,image_type,image_index,path,tag,COALESCE(mime_type,'') FROM images WHERE item_id=? ORDER BY image_type,image_index`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Image
	for rows.Next() {
		var img Image
		if err := rows.Scan(&img.ItemID, &img.Type, &img.Index, &img.Path, &img.Tag, &img.Mime); err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	return out, rows.Err()
}

func (s *Store) ImagesByItemIDs(ids []string) (map[string][]Image, error) {
	out := map[string][]Image{}
	for _, ids := range chunks(ids, 900) {
		args := anys(ids)
		rows, err := s.DB.Query(`SELECT item_id,image_type,image_index,path,tag,COALESCE(mime_type,'') FROM images WHERE item_id IN (`+marksN(len(ids))+`) ORDER BY item_id,image_type,image_index`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var img Image
			if err := rows.Scan(&img.ItemID, &img.Type, &img.Index, &img.Path, &img.Tag, &img.Mime); err != nil {
				rows.Close()
				return nil, err
			}
			out[img.ItemID] = append(out[img.ItemID], img)
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

func (s *Store) ItemsByIDs(ids []string) (map[string]Item, error) {
	out := map[string]Item{}
	for _, ids := range chunks(ids, 900) {
		rows, err := s.DB.Query(selectItem+` WHERE i.id IN (`+marksN(len(ids))+`)`, anys(ids)...)
		if err != nil {
			return nil, err
		}
		items, err := scanItems(rows)
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			out[item.ID] = item
		}
	}
	return out, nil
}

// FolderStat summarises what sits beneath a folder item.
type FolderStat struct {
	ChildCount, RecursiveItemCount int
	RuntimeTicks                   int64
}

// maxFolderDepth bounds the descent below a folder. The tree only ever runs
// library -> artist -> album -> track, so the cap costs nothing and stops a
// cycle in parent_id from spinning the recursive query forever.
const maxFolderDepth = 8

// FolderStats reports, for each of ids in a single pass, how many direct
// children it has, how many media files sit anywhere below it, and how long
// they run. Those are the numbers behind "12 tracks", "62 episodes" and an
// album's total duration. Asking per item would mean one query per row of
// every listing.
func (s *Store) FolderStats(ids []string) (map[string]FolderStat, error) {
	out := map[string]FolderStat{}
	for _, ids := range chunks(ids, 900) {
		rows, err := s.DB.Query(`WITH RECURSIVE tree(root,id,depth) AS (
				SELECT id,id,0 FROM items WHERE id IN (`+marksN(len(ids))+`)
				UNION ALL
				SELECT t.root,i.id,t.depth+1 FROM items i JOIN tree t ON i.parent_id=t.id WHERE t.depth<?
			)
			SELECT t.root,
				SUM(CASE WHEN t.depth=1 THEN 1 ELSE 0 END),
				SUM(CASE WHEN t.depth>0 AND i.is_folder=0 THEN 1 ELSE 0 END),
				SUM(CASE WHEN t.depth>0 THEN COALESCE(i.runtime_ticks,0) ELSE 0 END)
			FROM tree t JOIN items i ON i.id=t.id
			GROUP BY t.root`, append(anys(ids), maxFolderDepth)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var stat FolderStat
			if err := rows.Scan(&id, &stat.ChildCount, &stat.RecursiveItemCount, &stat.RuntimeTicks); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = stat
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

// itemFilter builds the WHERE fragment shared by Items and CountItems so that a
// listing and its reported total can never drift apart. It returns a fragment
// meant to follow "WHERE 1=1", which keeps every clause below uniform.
func itemFilter(q ItemQuery) (string, []any) {
	sqlq := ""
	var args []any
	if q.ParentID != "" {
		if q.Recursive {
			sqlq += ` AND (i.parent_id=? OR i.library_id=?)`
			args = append(args, q.ParentID, q.ParentID)
		} else {
			sqlq += ` AND i.parent_id=?`
			args = append(args, q.ParentID)
		}
	}
	if q.Type != "" {
		sqlq += ` AND i.type IN (` + marks(q.Type) + `)`
		for _, t := range split(q.Type) {
			args = append(args, t)
		}
	}
	if q.ExcludeTypes != "" {
		sqlq += ` AND i.type NOT IN (` + marks(q.ExcludeTypes) + `)`
		for _, t := range split(q.ExcludeTypes) {
			args = append(args, t)
		}
	}
	if q.Search != "" {
		sqlq += ` AND i.name LIKE ?`
		args = append(args, "%"+q.Search+"%")
	}
	if q.Name != "" {
		sqlq += ` AND i.name=? COLLATE NOCASE`
		args = append(args, q.Name)
	}
	if q.NameStartsWith != "" {
		sqlq += ` AND i.name LIKE ? COLLATE NOCASE`
		args = append(args, q.NameStartsWith+"%")
	}
	if q.Genres != "" {
		sqlq += ` AND (`
		for i, g := range split(strings.ReplaceAll(q.Genres, "|", ",")) {
			if i > 0 {
				sqlq += ` OR `
			}
			sqlq += `i.genres_json LIKE ?`
			args = append(args, "%\""+g+"\"%")
		}
		sqlq += `)`
	}
	if q.OfficialRatings != "" {
		ratings := strings.ReplaceAll(q.OfficialRatings, "|", ",")
		sqlq += ` AND i.official_rating IN (` + marks(ratings) + `)`
		for _, r := range split(ratings) {
			args = append(args, r)
		}
	}
	if q.Years != "" {
		sqlq += ` AND i.production_year IN (` + marks(q.Years) + `)`
		for _, y := range split(q.Years) {
			args = append(args, y)
		}
	}
	if q.IDs != "" {
		sqlq += ` AND i.id IN (` + marks(q.IDs) + `)`
		for _, id := range split(q.IDs) {
			args = append(args, id)
		}
	}
	if q.ArtistIDs != "" {
		// An album is a child of its artist and a track is a grandchild, so
		// matching both parent levels answers "everything by this artist"
		// without joining the tree twice.
		placeholders := marks(q.ArtistIDs)
		sqlq += ` AND (i.parent_id IN (` + placeholders + `) OR i.parent_id IN (SELECT id FROM items WHERE parent_id IN (` + placeholders + `)))`
		for range 2 {
			for _, id := range split(q.ArtistIDs) {
				args = append(args, id)
			}
		}
	}
	if q.PersonIDs != "" {
		sqlq += ` AND i.id IN (SELECT item_id FROM item_people WHERE person_id IN (` + marks(q.PersonIDs) + `))`
		for _, id := range split(q.PersonIDs) {
			args = append(args, id)
		}
	}
	if q.Favorite || q.Played || q.Unplayed {
		if q.UserID == "" {
			sqlq += ` AND 0`
		}
		if q.Favorite {
			sqlq += ` AND i.id IN (SELECT item_id FROM playback_state WHERE user_id=? AND is_favorite=1)`
			args = append(args, q.UserID)
		}
		if q.Played {
			sqlq += ` AND i.id IN (SELECT item_id FROM playback_state WHERE user_id=? AND played=1)`
			args = append(args, q.UserID)
		}
		if q.Unplayed {
			sqlq += ` AND i.id NOT IN (SELECT item_id FROM playback_state WHERE user_id=? AND played=1)`
			args = append(args, q.UserID)
		}
	}
	return sqlq, args
}

func (s *Store) Items(q ItemQuery) ([]Item, error) {
	where, args := itemFilter(q)
	order, orderArgs := orderBy(q)
	sqlq := selectItem + ` WHERE 1=1` + where + order
	args = append(args, orderArgs...)
	switch {
	case q.Limit > 0:
		sqlq += ` LIMIT ?`
		args = append(args, q.Limit)
	case q.Start > 0:
		// SQLite rejects OFFSET on its own, and -1 is its "no limit" limit.
		sqlq += ` LIMIT -1`
	}
	if q.Start > 0 {
		sqlq += ` OFFSET ?`
		args = append(args, q.Start)
	}
	rows, err := s.DB.Query(sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

// CountItems reports how many items match q, ignoring its paging. Clients read
// the unpaged total to decide whether another page exists, so a count that
// stopped at the limit would end their scrolling after the first page.
func (s *Store) CountItems(q ItemQuery) (int, error) {
	where, args := itemFilter(q)
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM items i WHERE 1=1`+where, args...).Scan(&n)
	return n, err
}

func (s *Store) EpisodesForSeries(seriesID string) ([]Item, error) {
	rows, err := s.DB.Query(selectItem+` JOIN items se ON se.id=i.parent_id
		WHERE i.type='Episode' AND se.parent_id=?
		ORDER BY COALESCE(i.parent_index_number,se.index_number,0), COALESCE(i.index_number,0), i.sort_name`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

func (s *Store) CountItemsByType(typ string) (int, error) {
	q := `SELECT COUNT(*) FROM items`
	var args []any
	if typ != "" {
		q += ` WHERE type IN (` + marks(typ) + `)`
		for _, t := range split(typ) {
			args = append(args, t)
		}
	}
	var n int
	err := s.DB.QueryRow(q, args...).Scan(&n)
	return n, err
}

// Filters lists the genres, ratings and years found in one library, or across
// the video libraries when libraryID is empty.
func (s *Store) Filters(libraryID string) (genres, ratings []string, years []int, err error) {
	q, args := `SELECT COALESCE(genres_json,''), COALESCE(official_rating,''), COALESCE(production_year,0) FROM items WHERE type IN ('Movie','Series','Episode')`, []any{}
	if libraryID != "" {
		q, args = `SELECT COALESCE(genres_json,''), COALESCE(official_rating,''), COALESCE(production_year,0) FROM items WHERE library_id=? AND (is_folder=0 OR type IN ('Series','MusicAlbum'))`, []any{libraryID}
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	gs, rs, ys := map[string]bool{}, map[string]bool{}, map[int]bool{}
	genres, ratings, years = []string{}, []string{}, []int{}
	for rows.Next() {
		var gj, rating string
		var year int
		if err := rows.Scan(&gj, &rating, &year); err != nil {
			return nil, nil, nil, err
		}
		var names []string
		_ = json.Unmarshal([]byte(gj), &names)
		for _, n := range names {
			if n != "" {
				gs[n] = true
			}
		}
		if rating != "" {
			rs[rating] = true
		}
		if year != 0 {
			ys[year] = true
		}
	}
	for g := range gs {
		genres = append(genres, g)
	}
	for r := range rs {
		ratings = append(ratings, r)
	}
	for y := range ys {
		years = append(years, y)
	}
	sort.Strings(genres)
	sort.Strings(ratings)
	sort.Ints(years)
	return genres, ratings, years, rows.Err()
}

func (s *Store) Item(id string) (Item, error) {
	rows, err := s.DB.Query(selectItem+` WHERE i.id=?`, id)
	if err != nil {
		return Item{}, err
	}
	defer rows.Close()
	items, err := scanItems(rows)
	if err != nil {
		return Item{}, err
	}
	if len(items) == 0 {
		return Item{}, sql.ErrNoRows
	}
	return items[0], nil
}

func (s *Store) SaveProgress(userID, itemID string, pos int64, played bool) error {
	now := time.Now().UTC().Format(time.RFC3339)
	p := 0
	if played {
		p = 1
	}
	_, err := s.DB.Exec(`INSERT INTO playback_state(user_id,item_id,played,play_count,playback_position_ticks,last_played_at,updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(user_id,item_id) DO UPDATE SET played=excluded.played,play_count=playback_state.play_count+CASE WHEN excluded.played=1 AND playback_state.played=0 THEN 1 ELSE 0 END,playback_position_ticks=excluded.playback_position_ticks,last_played_at=excluded.last_played_at,updated_at=excluded.updated_at`, userID, itemID, p, p, pos, now, now)
	return err
}

type Playback struct {
	PositionTicks int64
	PlayCount     int
	Played        bool
	Favorite      bool
	Likes         *bool
}

func (s *Store) Playback(userID, itemID string) Playback {
	var p Playback
	var played, favorite int
	var likes sql.NullBool
	_ = s.DB.QueryRow(`SELECT playback_position_ticks,play_count,played,is_favorite,likes FROM playback_state WHERE user_id=? AND item_id=?`, userID, itemID).Scan(&p.PositionTicks, &p.PlayCount, &played, &favorite, &likes)
	p.Played = played == 1
	p.Favorite = favorite == 1
	if likes.Valid {
		p.Likes = &likes.Bool
	}
	return p
}

func (s *Store) PlaybackByItemIDs(userID string, ids []string) (map[string]Playback, error) {
	out := map[string]Playback{}
	if userID == "" || len(ids) == 0 {
		return out, nil
	}
	for _, ids := range chunks(ids, 900) {
		args := append([]any{userID}, anys(ids)...)
		rows, err := s.DB.Query(`SELECT item_id,playback_position_ticks,play_count,played,is_favorite,likes FROM playback_state WHERE user_id=? AND item_id IN (`+marksN(len(ids))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var p Playback
			var played, favorite int
			var likes sql.NullBool
			if err := rows.Scan(&id, &p.PositionTicks, &p.PlayCount, &played, &favorite, &likes); err != nil {
				rows.Close()
				return nil, err
			}
			p.Played = played == 1
			p.Favorite = favorite == 1
			if likes.Valid {
				p.Likes = &likes.Bool
			}
			out[id] = p
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

func (s *Store) SetFavorite(userID, itemID string, favorite bool) error {
	now := time.Now().UTC().Format(time.RFC3339)
	f := 0
	if favorite {
		f = 1
	}
	_, err := s.DB.Exec(`INSERT INTO playback_state(user_id,item_id,is_favorite,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(user_id,item_id) DO UPDATE SET is_favorite=excluded.is_favorite,updated_at=excluded.updated_at`, userID, itemID, f, now)
	return err
}

func (s *Store) SetRating(userID, itemID string, likes *bool) error {
	now := time.Now().UTC().Format(time.RFC3339)
	var v any
	if likes != nil {
		v = *likes
	}
	_, err := s.DB.Exec(`INSERT INTO playback_state(user_id,item_id,likes,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(user_id,item_id) DO UPDATE SET likes=excluded.likes,updated_at=excluded.updated_at`, userID, itemID, v, now)
	return err
}

func (s *Store) Resume(userID, parentID, types string, limit int) ([]Item, error) {
	q := selectItem + ` JOIN playback_state ps ON ps.item_id=i.id WHERE ps.user_id=? AND ps.playback_position_ticks>0 AND ps.played=0`
	args := []any{userID}
	if parentID != "" {
		q += ` AND (i.library_id=? OR i.parent_id=?)`
		args = append(args, parentID, parentID)
	}
	if types != "" {
		q += ` AND i.type IN (` + marks(types) + `)`
		for _, typ := range split(types) {
			args = append(args, typ)
		}
	}
	q += ` ORDER BY ps.updated_at DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

func (s *Store) NextUp(userID, seriesID, parentID string, start, limit int) ([]Item, error) {
	q := selectItem + ` JOIN items se ON se.id=i.parent_id JOIN items sr ON sr.id=se.parent_id
		WHERE i.type='Episode'
		AND i.id NOT IN (SELECT item_id FROM playback_state WHERE user_id=? AND played=1)
		AND NOT EXISTS (
			SELECT 1 FROM items e2 JOIN items se2 ON se2.id=e2.parent_id
			WHERE e2.type='Episode' AND se2.parent_id=sr.id
			AND e2.id NOT IN (SELECT item_id FROM playback_state WHERE user_id=? AND played=1)
			AND (
				COALESCE(e2.parent_index_number,se2.index_number,0) < COALESCE(i.parent_index_number,se.index_number,0)
				OR (COALESCE(e2.parent_index_number,se2.index_number,0)=COALESCE(i.parent_index_number,se.index_number,0) AND COALESCE(e2.index_number,0) < COALESCE(i.index_number,0))
				OR (COALESCE(e2.parent_index_number,se2.index_number,0)=COALESCE(i.parent_index_number,se.index_number,0) AND COALESCE(e2.index_number,0)=COALESCE(i.index_number,0) AND e2.sort_name < i.sort_name)
			)
		)`
	args := []any{userID, userID}
	if seriesID != "" {
		q += ` AND sr.id=?`
		args = append(args, seriesID)
	}
	if parentID != "" {
		q += ` AND (sr.parent_id=? OR sr.id=? OR se.id=?)`
		args = append(args, parentID, parentID, parentID)
	}
	q += ` ORDER BY sr.sort_name, COALESCE(i.parent_index_number,se.index_number,0), COALESCE(i.index_number,0), i.sort_name`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	if start > 0 {
		q += ` OFFSET ?`
		args = append(args, start)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

func (s *Store) AddUserPolicy(name, pass string, admin, child bool, maxRating int) error {
	h, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	a, c := 0, 0
	if admin {
		a = 1
	}
	if child {
		c = 1
	}
	_, err = s.DB.Exec(`INSERT INTO users(id,name,password_hash,is_admin,is_child,max_parental_rating,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET password_hash=excluded.password_hash,is_admin=excluded.is_admin,is_child=excluded.is_child,max_parental_rating=excluded.max_parental_rating,updated_at=excluded.updated_at`, StableID("user", name), name, string(h), a, c, maxRating, now, now)
	return err
}

func (s *Store) AuthDevice(name, pass, deviceID, deviceName, client string) (User, string, error) {
	var u User
	var hash string
	var admin, child int
	err := s.DB.QueryRow(`SELECT id,name,password_hash,is_admin,is_child,max_parental_rating FROM users WHERE name=?`, name).Scan(&u.ID, &u.Name, &hash, &admin, &child, &u.MaxParentalRating)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_ = bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte(pass))
			return u, "", ErrInvalidCredentials
		}
		return u, "", err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass)) != nil {
		return u, "", ErrInvalidCredentials
	}
	u.IsAdmin = admin == 1
	u.IsChild = child == 1
	token := randToken()
	_, err = s.DB.Exec(`INSERT INTO auth_tokens(token_hash,user_id,device_id,device_name,client_name,created_at,last_seen_at) VALUES(?,?,?,?,?,?,?)`, tokenHash(token), u.ID, deviceID, deviceName, client, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	return u, token, err
}

func (s *Store) PersonByID(id string) (Person, error) {
	rows, err := s.DB.Query(selectPerson+` FROM people p WHERE p.id=? LIMIT 1`, id)
	if err != nil {
		return Person{}, err
	}
	defer rows.Close()
	people, err := scanPeople(rows)
	if err != nil {
		return Person{}, err
	}
	if len(people) == 0 {
		return Person{}, sql.ErrNoRows
	}
	return people[0], nil
}

func (s *Store) UserByTokenContext(ctx context.Context, token string) (User, error) {
	var u User
	var admin, child int
	hash := tokenHash(token)
	err := s.DB.QueryRowContext(ctx, `SELECT u.id,u.name,u.is_admin,u.is_child,u.max_parental_rating FROM users u JOIN auth_tokens t ON t.user_id=u.id WHERE t.token_hash=?`, hash).Scan(&u.ID, &u.Name, &admin, &child, &u.MaxParentalRating)
	if err == nil {
		_, _ = s.DB.ExecContext(ctx, `UPDATE auth_tokens SET last_seen_at=? WHERE token_hash=?`, time.Now().UTC().Format(time.RFC3339), hash)
	}
	u.IsAdmin = admin == 1
	u.IsChild = child == 1
	return u, err
}

func (s *Store) DeleteToken(token string) error {
	_, err := s.DB.Exec(`DELETE FROM auth_tokens WHERE token_hash=?`, tokenHash(token))
	return err
}

func (s *Store) Sessions(userID string) ([]Session, error) {
	rows, err := s.DB.Query(`SELECT t.token_hash,u.id,u.name,t.device_id,t.device_name,t.client_name FROM auth_tokens t JOIN users u ON u.id=t.user_id WHERE u.id=? ORDER BY t.last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var ss Session
		if err := rows.Scan(&ss.ID, &ss.UserID, &ss.UserName, &ss.DeviceID, &ss.DeviceName, &ss.Client); err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

func (s *Store) addColumn(table, col string) error {
	name := strings.Fields(col)[0]
	rows, err := s.DB.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var got, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &got, &typ, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if got == name {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.DB.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + col)
	return err
}

func (s *Store) PublicUsers() ([]User, error) {
	rows, err := s.DB.Query(`SELECT id,name,is_admin,is_child,max_parental_rating FROM users ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var a, c int
		if err := rows.Scan(&u.ID, &u.Name, &a, &c, &u.MaxParentalRating); err != nil {
			return nil, err
		}
		u.IsAdmin = a == 1
		u.IsChild = c == 1
		out = append(out, u)
	}
	return out, rows.Err()
}

func scanItems(rows *sql.Rows) ([]Item, error) {
	var out []Item
	for rows.Next() {
		var it Item
		var folder, lyrics int
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.ParentID, &it.Type, &it.Name, &it.SortName, &it.Path, &it.RelativePath, &folder, &it.ProductionYear, &it.PremiereDate, &it.Overview, &it.IndexNumber, &it.ParentIndexNumber, &it.DateCreated, &it.ProviderIDsJSON, &it.GenresJSON, &it.StudiosJSON, &it.PeopleJSON, &it.TaglinesJSON, &it.ExternalURLsJSON, &it.CommunityRating, &it.OfficialRating, &it.RuntimeTicks, &it.LastSeenScan, &it.Album, &it.AlbumArtist, &it.ArtistsJSON, &lyrics, &it.Size, &it.MTimeUnix, &it.Container); err != nil {
			return nil, err
		}
		it.IsFolder, it.HasLyrics = folder == 1, lyrics == 1
		out = append(out, it)
	}
	return out, rows.Err()
}

func scanPeople(rows *sql.Rows) ([]Person, error) {
	var out []Person
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.TMDBID, &p.IMDBID, &p.Name, &p.ProfileURL, &p.Biography, &p.BirthDate, &p.DeathDate, &p.PlaceOfBirth, &p.KnownDepartment, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const selectItem = `SELECT i.id,i.library_id,COALESCE(i.parent_id,''),i.type,i.name,i.sort_name,COALESCE(i.path,''),COALESCE(i.relative_path,''),i.is_folder,COALESCE(i.production_year,0),COALESCE(i.premiere_date,''),COALESCE(i.overview,''),COALESCE(i.index_number,0),COALESCE(i.parent_index_number,0),i.date_created,COALESCE(i.provider_ids_json,''),COALESCE(i.genres_json,''),COALESCE(i.studios_json,''),COALESCE(i.people_json,''),COALESCE(i.taglines_json,''),COALESCE(i.external_urls_json,''),COALESCE(i.community_rating,0),COALESCE(i.official_rating,''),COALESCE(i.runtime_ticks,0),COALESCE(i.last_seen_scan,''),COALESCE(i.album,''),COALESCE(i.album_artist,''),COALESCE(i.artists_json,''),COALESCE(i.has_lyrics,0),COALESCE(ms.size_bytes,0),COALESCE(ms.mtime_unix,0),COALESCE(ms.container,'') FROM items i LEFT JOIN media_sources ms ON ms.item_id=i.id`
const selectPerson = `SELECT p.id,COALESCE(p.tmdb_id,0),COALESCE(p.imdb_id,''),p.name,COALESCE(p.profile_url,''),COALESCE(p.biography,''),COALESCE(p.birth_date,''),COALESCE(p.death_date,''),COALESCE(p.place_of_birth,''),COALESCE(p.known_for_department,''),p.updated_at`

func StableID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:16])
}
func Ext(p string) string { return strings.TrimPrefix(strings.ToLower(filepath.Ext(p)), ".") }
func JSON(v any) string   { b, _ := json.Marshal(v); return string(b) }
func nullEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func nullZero(i int) any {
	if i == 0 {
		return nil
	}
	return i
}
func nullInt64(i int64) any {
	if i == 0 {
		return nil
	}
	return i
}
func nullFloat(f float64) any {
	if f == 0 {
		return nil
	}
	return f
}
func randToken() string         { var b [32]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func tokenHash(t string) string { h := sha256.Sum256([]byte(t)); return hex.EncodeToString(h[:]) }
func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
func marks(s string) string { return strings.TrimRight(strings.Repeat("?,", len(split(s))), ",") }
func marksN(n int) string   { return strings.TrimRight(strings.Repeat("?,", n), ",") }
func anys(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
func chunks(ss []string, n int) [][]string {
	var out [][]string
	for len(ss) > 0 {
		if len(ss) < n {
			n = len(ss)
		}
		out = append(out, ss[:n])
		ss = ss[n:]
	}
	return out
}

// orderBy returns the ORDER BY clause for q and the arguments it binds. Only
// the per-user sorts bind any: they read the caller's playback row.
func orderBy(q ItemQuery) (string, []any) {
	desc := ""
	if strings.EqualFold(q.SortOrder, "Descending") || strings.EqualFold(q.SortOrder, "desc") {
		desc = " DESC"
	}
	switch strings.Split(q.SortBy, ",")[0] {
	case "DatePlayed":
		return ` ORDER BY (SELECT last_played_at FROM playback_state WHERE user_id=? AND item_id=i.id)` + desc + `, i.sort_name`, []any{q.UserID}
	case "PlayCount":
		return ` ORDER BY COALESCE((SELECT play_count FROM playback_state WHERE user_id=? AND item_id=i.id),0)` + desc + `, i.sort_name`, []any{q.UserID}
	}
	return orderByColumn(q.SortBy, desc), nil
}

func orderByColumn(by, desc string) string {
	switch strings.Split(by, ",")[0] {
	case "Runtime":
		return ` ORDER BY COALESCE(i.runtime_ticks,0)` + desc + `, i.sort_name`
	case "DateCreated":
		return ` ORDER BY i.date_created` + desc + `, i.sort_name`
	case "ProductionYear":
		return ` ORDER BY COALESCE(i.production_year,0)` + desc + `, i.sort_name`
	case "PremiereDate":
		return ` ORDER BY COALESCE(i.premiere_date,'')` + desc + `, i.sort_name`
	case "CommunityRating":
		return ` ORDER BY COALESCE(i.community_rating,0)` + desc + `, i.sort_name`
	case "SortName":
		return ` ORDER BY i.sort_name` + desc
	case "IndexNumber":
		return ` ORDER BY COALESCE(i.index_number,0)` + desc + `, i.sort_name`
	case "ParentIndexNumber":
		return ` ORDER BY COALESCE(i.parent_index_number,0)` + desc + `, COALESCE(i.index_number,0)` + desc + `, i.sort_name`
	case "Random":
		// Shuffle-all. The direction a client sends is meaningless here.
		return ` ORDER BY RANDOM()`
	default:
		return ` ORDER BY i.sort_name` + desc
	}
}

const schema = `
PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, is_admin INTEGER NOT NULL DEFAULT 0, is_child INTEGER NOT NULL DEFAULT 0, max_parental_rating INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS auth_tokens (token_hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, device_id TEXT NOT NULL DEFAULT '', device_name TEXT NOT NULL DEFAULT '', client_name TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, last_seen_at TEXT);
CREATE TABLE IF NOT EXISTS libraries (id TEXT PRIMARY KEY, name TEXT NOT NULL, collection_type TEXT NOT NULL, path TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS items (id TEXT PRIMARY KEY, library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE, parent_id TEXT REFERENCES items(id) ON DELETE CASCADE, type TEXT NOT NULL, name TEXT NOT NULL, sort_name TEXT NOT NULL, path TEXT, relative_path TEXT, is_folder INTEGER NOT NULL DEFAULT 0, production_year INTEGER, premiere_date TEXT, overview TEXT, runtime_ticks INTEGER, index_number INTEGER, parent_index_number INTEGER, date_created TEXT NOT NULL, provider_ids_json TEXT, last_seen_scan TEXT);
CREATE TABLE IF NOT EXISTS media_sources (id TEXT PRIMARY KEY, item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE, path TEXT NOT NULL, container TEXT NOT NULL DEFAULT '', size_bytes INTEGER NOT NULL DEFAULT 0, mtime_unix INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS images (id TEXT PRIMARY KEY, item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE, image_type TEXT NOT NULL, image_index INTEGER NOT NULL DEFAULT 0, path TEXT NOT NULL, tag TEXT NOT NULL, width INTEGER, height INTEGER, mime_type TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(item_id, image_type, image_index));
CREATE TABLE IF NOT EXISTS people (id TEXT PRIMARY KEY, tmdb_id INTEGER UNIQUE, imdb_id TEXT, name TEXT NOT NULL, profile_url TEXT, biography TEXT, birth_date TEXT, death_date TEXT, place_of_birth TEXT, known_for_department TEXT, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS item_people (item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE, person_id TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE, role TEXT NOT NULL, character TEXT NOT NULL DEFAULT '', sort_order INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(item_id, person_id, role, character));
CREATE TABLE IF NOT EXISTS playlist_entries (entry_id TEXT PRIMARY KEY, playlist_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE, item_id TEXT NOT NULL, position INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS idx_playlist_entries ON playlist_entries(playlist_id, position);
CREATE TABLE IF NOT EXISTS playback_state (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, item_id TEXT NOT NULL, played INTEGER NOT NULL DEFAULT 0, play_count INTEGER NOT NULL DEFAULT 0, playback_position_ticks INTEGER NOT NULL DEFAULT 0, is_favorite INTEGER NOT NULL DEFAULT 0, likes INTEGER, last_played_at TEXT, updated_at TEXT NOT NULL, PRIMARY KEY(user_id, item_id));
CREATE INDEX IF NOT EXISTS idx_items_library_parent ON items(library_id, parent_id);
CREATE INDEX IF NOT EXISTS idx_items_parent_id ON items(parent_id);
CREATE INDEX IF NOT EXISTS idx_items_library_seen ON items(library_id, last_seen_scan);
CREATE INDEX IF NOT EXISTS idx_items_type ON items(type);
CREATE INDEX IF NOT EXISTS idx_items_sort_name ON items(sort_name);
CREATE INDEX IF NOT EXISTS idx_media_sources_item ON media_sources(item_id);
CREATE INDEX IF NOT EXISTS idx_media_sources_path ON media_sources(path);
CREATE INDEX IF NOT EXISTS idx_images_item ON images(item_id);
CREATE INDEX IF NOT EXISTS idx_people_name ON people(name COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS idx_item_people_item ON item_people(item_id, sort_order);
CREATE INDEX IF NOT EXISTS idx_item_people_person ON item_people(person_id);
CREATE INDEX IF NOT EXISTS idx_playback_favorite ON playback_state(user_id, is_favorite);
CREATE INDEX IF NOT EXISTS idx_playback_played ON playback_state(user_id, played);
`
