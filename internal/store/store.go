package store

import (
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

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/bcrypt"
)

type Store struct{ DB *sql.DB }

type User struct {
	ID, Name string
	IsAdmin  bool
}

type Library struct{ ID, Name, Type, Path string }

type Item struct {
	ID, LibraryID, ParentID, Type, Name, SortName, Path, RelativePath   string
	DateCreated, PremiereDate, Overview, ProviderIDsJSON                string
	GenresJSON, StudiosJSON, PeopleJSON, TaglinesJSON, ExternalURLsJSON string
	OfficialRating, Container                                           string
	IsFolder                                                            bool
	ProductionYear, IndexNumber, ParentIndexNumber                      int
	Size, RuntimeTicks                                                  int64
	CommunityRating                                                     float64
}

type Image struct {
	ItemID, Type, Path, Tag, Mime string
	Index                         int
}

type ItemQuery struct {
	ParentID, Type, Search, SortBy, SortOrder string
	Recursive                                 bool
	Start, Limit                              int
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_foreign_keys=on")
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
		"genres_json TEXT", "studios_json TEXT", "people_json TEXT", "taglines_json TEXT", "external_urls_json TEXT", "community_rating REAL", "official_rating TEXT",
	} {
		if err := s.addColumn("items", c); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveLibraries(libs []Library) error {
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(libs) == 0 {
		if _, err := tx.Exec(`DELETE FROM libraries`); err != nil {
			return err
		}
	} else {
		args := make([]any, len(libs))
		for i, l := range libs {
			args[i] = l.ID
		}
		if _, err := tx.Exec(`DELETE FROM libraries WHERE id NOT IN (`+strings.TrimRight(strings.Repeat("?,", len(libs)), ",")+`)`, args...); err != nil {
			return err
		}
	}
	for _, l := range libs {
		if l.ID == "" {
			l.ID = StableID("library", l.Path)
		}
		_, err = tx.Exec(`INSERT INTO libraries(id,name,collection_type,path,enabled,created_at,updated_at)
			VALUES(?,?,?,?,1,?,?)
			ON CONFLICT(path) DO UPDATE SET name=excluded.name, collection_type=excluded.collection_type, updated_at=excluded.updated_at`, l.ID, l.Name, l.Type, l.Path, now, now)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO items(id,library_id,type,name,sort_name,path,is_folder,date_created,date_modified)
			VALUES(?,?,?,?,?,?,1,?,?)
			ON CONFLICT(id) DO UPDATE SET name=excluded.name, sort_name=excluded.sort_name, path=excluded.path, date_modified=excluded.date_modified`, l.ID, l.ID, "CollectionFolder", l.Name, strings.ToLower(l.Name), l.Path, now, now)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Libraries() ([]Library, error) {
	rows, err := s.DB.Query(`SELECT id,name,collection_type,path FROM libraries WHERE enabled=1 ORDER BY name`)
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
	folder := 0
	if it.IsFolder {
		folder = 1
	}
	if it.SortName == "" {
		it.SortName = strings.ToLower(it.Name)
	}
	_, err := s.DB.Exec(`INSERT INTO items(id,library_id,parent_id,type,name,sort_name,path,relative_path,is_folder,production_year,premiere_date,overview,index_number,parent_index_number,date_created,date_modified,provider_ids_json,genres_json,studios_json,people_json,taglines_json,external_urls_json,community_rating,official_rating,runtime_ticks)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET parent_id=excluded.parent_id,type=excluded.type,name=excluded.name,sort_name=excluded.sort_name,path=excluded.path,relative_path=excluded.relative_path,is_folder=excluded.is_folder,production_year=COALESCE(excluded.production_year,items.production_year),premiere_date=COALESCE(excluded.premiere_date,items.premiere_date),overview=COALESCE(excluded.overview,items.overview),index_number=COALESCE(excluded.index_number,items.index_number),parent_index_number=COALESCE(excluded.parent_index_number,items.parent_index_number),date_modified=excluded.date_modified,provider_ids_json=COALESCE(excluded.provider_ids_json,items.provider_ids_json),genres_json=COALESCE(excluded.genres_json,items.genres_json),studios_json=COALESCE(excluded.studios_json,items.studios_json),people_json=COALESCE(excluded.people_json,items.people_json),taglines_json=COALESCE(excluded.taglines_json,items.taglines_json),external_urls_json=COALESCE(excluded.external_urls_json,items.external_urls_json),community_rating=COALESCE(excluded.community_rating,items.community_rating),official_rating=COALESCE(excluded.official_rating,items.official_rating),runtime_ticks=COALESCE(excluded.runtime_ticks,items.runtime_ticks)`,
		it.ID, it.LibraryID, nullEmpty(it.ParentID), it.Type, it.Name, it.SortName, nullEmpty(it.Path), nullEmpty(it.RelativePath), folder, nullZero(it.ProductionYear), nullEmpty(it.PremiereDate), nullEmpty(it.Overview), nullZero(it.IndexNumber), nullZero(it.ParentIndexNumber), now, now, nullEmpty(it.ProviderIDsJSON), nullEmpty(it.GenresJSON), nullEmpty(it.StudiosJSON), nullEmpty(it.PeopleJSON), nullEmpty(it.TaglinesJSON), nullEmpty(it.ExternalURLsJSON), nullFloat(it.CommunityRating), nullEmpty(it.OfficialRating), nullInt64(it.RuntimeTicks))
	if err != nil {
		return err
	}
	if !it.IsFolder && it.Path != "" {
		_, err = s.DB.Exec(`INSERT INTO media_sources(id,item_id,path,container,size_bytes,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET path=excluded.path, container=excluded.container, size_bytes=excluded.size_bytes, updated_at=excluded.updated_at`, StableID("source", it.ID), it.ID, it.Path, it.Container, it.Size, now, now)
	}
	return err
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

func (s *Store) Items(q ItemQuery) ([]Item, error) {
	sqlq := selectItem + ` WHERE 1=1`
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
	if q.Search != "" {
		sqlq += ` AND i.name LIKE ?`
		args = append(args, "%"+q.Search+"%")
	}
	sqlq += orderBy(q.SortBy, q.SortOrder)
	if q.Limit > 0 {
		sqlq += ` LIMIT ?`
		args = append(args, q.Limit)
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

func (s *Store) Filters() (genres, ratings []string, err error) {
	rows, err := s.DB.Query(`SELECT COALESCE(genres_json,''), COALESCE(official_rating,'') FROM items WHERE type IN ('Movie','Series','Episode')`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	gs, rs := map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var gj, rating string
		if err := rows.Scan(&gj, &rating); err != nil {
			return nil, nil, err
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
	}
	for g := range gs {
		genres = append(genres, g)
	}
	for r := range rs {
		ratings = append(ratings, r)
	}
	sort.Strings(genres)
	sort.Strings(ratings)
	return genres, ratings, rows.Err()
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
		ON CONFLICT(user_id,item_id) DO UPDATE SET played=excluded.played,playback_position_ticks=excluded.playback_position_ticks,last_played_at=excluded.last_played_at,updated_at=excluded.updated_at`, userID, itemID, p, p, pos, now, now)
	return err
}

type Playback struct {
	PositionTicks int64
	PlayCount     int
	Played        bool
}

func (s *Store) Playback(userID, itemID string) Playback {
	var p Playback
	var played int
	_ = s.DB.QueryRow(`SELECT playback_position_ticks,play_count,played FROM playback_state WHERE user_id=? AND item_id=?`, userID, itemID).Scan(&p.PositionTicks, &p.PlayCount, &played)
	p.Played = played == 1
	return p
}

func (s *Store) Resume(userID string, limit int) ([]Item, error) {
	q := selectItem + ` JOIN playback_state ps ON ps.item_id=i.id WHERE ps.user_id=? AND ps.playback_position_ticks>0 AND ps.played=0 ORDER BY ps.updated_at DESC`
	args := []any{userID}
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

func (s *Store) AddUser(name, pass string) error {
	h, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.DB.Exec(`INSERT INTO users(id,name,password_hash,is_admin,created_at,updated_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET password_hash=excluded.password_hash, updated_at=excluded.updated_at`, StableID("user", name), name, string(h), 1, now, now)
	return err
}

func (s *Store) Auth(name, pass string) (User, string, error) {
	var u User
	var hash string
	var admin int
	err := s.DB.QueryRow(`SELECT id,name,password_hash,is_admin FROM users WHERE name=?`, name).Scan(&u.ID, &u.Name, &hash, &admin)
	if err != nil {
		return u, "", err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass)) != nil {
		return u, "", errors.New("bad password")
	}
	u.IsAdmin = admin == 1
	token := randToken()
	_, err = s.DB.Exec(`INSERT INTO auth_tokens(token_hash,user_id,created_at,last_seen_at) VALUES(?,?,?,?)`, tokenHash(token), u.ID, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	return u, token, err
}

func (s *Store) UserByToken(token string) (User, error) {
	var u User
	var admin int
	err := s.DB.QueryRow(`SELECT u.id,u.name,u.is_admin FROM users u JOIN auth_tokens t ON t.user_id=u.id WHERE t.token_hash=?`, tokenHash(token)).Scan(&u.ID, &u.Name, &admin)
	u.IsAdmin = admin == 1
	return u, err
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
	rows, err := s.DB.Query(`SELECT id,name,is_admin FROM users ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var a int
		if err := rows.Scan(&u.ID, &u.Name, &a); err != nil {
			return nil, err
		}
		u.IsAdmin = a == 1
		out = append(out, u)
	}
	return out, rows.Err()
}

func scanItems(rows *sql.Rows) ([]Item, error) {
	var out []Item
	for rows.Next() {
		var it Item
		var folder int
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.ParentID, &it.Type, &it.Name, &it.SortName, &it.Path, &it.RelativePath, &folder, &it.ProductionYear, &it.PremiereDate, &it.Overview, &it.IndexNumber, &it.ParentIndexNumber, &it.DateCreated, &it.ProviderIDsJSON, &it.GenresJSON, &it.StudiosJSON, &it.PeopleJSON, &it.TaglinesJSON, &it.ExternalURLsJSON, &it.CommunityRating, &it.OfficialRating, &it.RuntimeTicks, &it.Size, &it.Container); err != nil {
			return nil, err
		}
		it.IsFolder = folder == 1
		out = append(out, it)
	}
	return out, rows.Err()
}

const selectItem = `SELECT i.id,i.library_id,COALESCE(i.parent_id,''),i.type,i.name,i.sort_name,COALESCE(i.path,''),COALESCE(i.relative_path,''),i.is_folder,COALESCE(i.production_year,0),COALESCE(i.premiere_date,''),COALESCE(i.overview,''),COALESCE(i.index_number,0),COALESCE(i.parent_index_number,0),i.date_created,COALESCE(i.provider_ids_json,''),COALESCE(i.genres_json,''),COALESCE(i.studios_json,''),COALESCE(i.people_json,''),COALESCE(i.taglines_json,''),COALESCE(i.external_urls_json,''),COALESCE(i.community_rating,0),COALESCE(i.official_rating,''),COALESCE(i.runtime_ticks,0),COALESCE(ms.size_bytes,0),COALESCE(ms.container,'') FROM items i LEFT JOIN media_sources ms ON ms.item_id=i.id`

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
func orderBy(by, dir string) string {
	desc := ""
	if strings.EqualFold(dir, "Descending") || strings.EqualFold(dir, "desc") {
		desc = " DESC"
	}
	switch strings.Split(by, ",")[0] {
	case "DateCreated":
		return ` ORDER BY i.date_created` + desc + `, i.sort_name`
	case "ProductionYear":
		return ` ORDER BY COALESCE(i.production_year,0)` + desc + `, i.sort_name`
	case "CommunityRating":
		return ` ORDER BY COALESCE(i.community_rating,0)` + desc + `, i.sort_name`
	default:
		return ` ORDER BY i.sort_name` + desc
	}
}

const schema = `
PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, is_admin INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS auth_tokens (token_hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, device_id TEXT NOT NULL DEFAULT '', device_name TEXT NOT NULL DEFAULT '', client_name TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, last_seen_at TEXT, expires_at TEXT);
CREATE TABLE IF NOT EXISTS libraries (id TEXT PRIMARY KEY, name TEXT NOT NULL, collection_type TEXT NOT NULL, path TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, last_scanned_at TEXT);
CREATE TABLE IF NOT EXISTS items (id TEXT PRIMARY KEY, library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE, parent_id TEXT REFERENCES items(id) ON DELETE CASCADE, type TEXT NOT NULL, name TEXT NOT NULL, sort_name TEXT NOT NULL, path TEXT, relative_path TEXT, is_folder INTEGER NOT NULL DEFAULT 0, production_year INTEGER, premiere_date TEXT, overview TEXT, runtime_ticks INTEGER, index_number INTEGER, parent_index_number INTEGER, date_created TEXT NOT NULL, date_modified TEXT, provider_ids_json TEXT, extra_json TEXT);
CREATE TABLE IF NOT EXISTS metadata_cache (provider TEXT NOT NULL, provider_key TEXT NOT NULL, media_type TEXT NOT NULL, language TEXT NOT NULL DEFAULT '', payload_json TEXT NOT NULL, fetched_at TEXT NOT NULL, expires_at TEXT, PRIMARY KEY(provider, provider_key, media_type, language));
CREATE TABLE IF NOT EXISTS media_sources (id TEXT PRIMARY KEY, item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE, path TEXT NOT NULL, container TEXT NOT NULL DEFAULT '', size_bytes INTEGER NOT NULL DEFAULT 0, mtime_unix INTEGER NOT NULL DEFAULT 0, bitrate INTEGER, width INTEGER, height INTEGER, duration_ticks INTEGER, media_streams_json TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS images (id TEXT PRIMARY KEY, item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE, image_type TEXT NOT NULL, image_index INTEGER NOT NULL DEFAULT 0, path TEXT NOT NULL, tag TEXT NOT NULL, width INTEGER, height INTEGER, mime_type TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(item_id, image_type, image_index));
CREATE TABLE IF NOT EXISTS playback_state (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE, played INTEGER NOT NULL DEFAULT 0, play_count INTEGER NOT NULL DEFAULT 0, playback_position_ticks INTEGER NOT NULL DEFAULT 0, last_played_at TEXT, updated_at TEXT NOT NULL, PRIMARY KEY(user_id, item_id));
CREATE INDEX IF NOT EXISTS idx_items_library_parent ON items(library_id, parent_id);
CREATE INDEX IF NOT EXISTS idx_items_type ON items(type);
CREATE INDEX IF NOT EXISTS idx_items_sort_name ON items(sort_name);
CREATE INDEX IF NOT EXISTS idx_media_sources_item ON media_sources(item_id);
CREATE INDEX IF NOT EXISTS idx_images_item ON images(item_id);
`
