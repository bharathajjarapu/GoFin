package library

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gofin/internal/audio"
	"gofin/internal/config"
	"gofin/internal/metadata"
	"gofin/internal/store"
)

type Scanner struct {
	Store *store.Store
	Meta  metadata.Client
	// CoverDir caches art extracted from audio files. Leaving it empty turns
	// embedded cover art off, which is what the tests without a database
	// directory rely on.
	CoverDir string
	scanID   string
	series   map[string]metadata.Result
	seasons  map[string]metadata.Result
	covers   map[string]bool
}

var videoExt = map[string]bool{"mkv": true, "mp4": true, "m4v": true, "avi": true, "mov": true, "webm": true}
var imageExt = map[string]string{"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp"}

// playable reports whether a library of this type should index the extension.
// The sets stay separate so that an .mp4 in a music library is ignored rather
// than indexed as a track; Jellyfin expects audio in MP4 to be named .m4a.
func playable(libraryType, ext string) bool {
	if libraryType == "music" {
		return audio.Supported(ext)
	}
	return videoExt[ext]
}

var epRe = regexp.MustCompile(`(?i)s(\d{1,2})e(\d{1,3})`)
var providerRe = regexp.MustCompile(`(?i)\[(tmdbid|imdbid|tvdbid)-([^\]]+)\]`)

// metadataNoMatch records that a metadata lookup was attempted without a match.
const metadataNoMatch = "{}"

func (s Scanner) ScanContext(ctx context.Context, libs []config.Library, only string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.series == nil {
		s.series = map[string]metadata.Result{}
		s.seasons = map[string]metadata.Result{}
	}
	if s.covers == nil {
		s.covers = map[string]bool{}
	}
	if s.scanID == "" {
		s.scanID = time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	allLibs := make([]store.Library, 0, len(libs))
	var slibs []store.Library
	for _, l := range libs {
		lib := store.Library{ID: store.StableID("library", l.Path), Name: l.Name, Type: l.Type, Path: l.Path}
		allLibs = append(allLibs, lib)
		if info, err := os.Stat(l.Path); err != nil || !info.IsDir() {
			continue
		}
		slibs = append(slibs, lib)
	}
	if err := s.Store.SaveLibraries(allLibs); err != nil {
		return err
	}
	for _, l := range slibs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if only != "" && only != l.ID && !strings.EqualFold(only, l.Name) {
			continue
		}
		if err := s.scanLibrary(ctx, l); err != nil {
			return err
		}
	}
	return s.pruneCovers()
}

func (s Scanner) scanLibrary(ctx context.Context, lib store.Library) error {
	root := filepath.Clean(lib.Path)
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil || d.IsDir() {
			return nil
		}
		ext := store.Ext(path)
		if !playable(lib.Type, ext) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		info, err := d.Info()
		if err != nil {
			return nil
		}
		mtime := info.ModTime().Unix()
		id := store.StableID("item", path)
		if s.Store.Unchanged(id, info.Size(), mtime) {
			switch lib.Type {
			case "music":
				// Music metadata is embedded, so an unchanged file can never
				// gain new tags. Keep its artist and album alive and move on.
				_ = s.touchTrackParents(id)
				return s.Store.TouchItem(id, s.scanID)
			case "tvshows":
				_ = s.touchEpisodeParents(lib, rel)
				// Files may have been first scanned before TMDB_API_KEY was set.
				// Retry TV items that still have no provider metadata.
				if s.needsMetadata(id) {
					return s.upsertEpisode(lib, path, rel, ext, info.Size(), mtime)
				}
			default:
				if s.needsMetadata(id) {
					return s.upsertMovie(lib, path, rel, ext, info.Size(), mtime)
				}
			}
			return s.Store.TouchItem(id, s.scanID)
		}
		switch lib.Type {
		case "music":
			return s.upsertTrack(lib, path, rel, ext, info.Size(), mtime)
		case "tvshows":
			return s.upsertEpisode(lib, path, rel, ext, info.Size(), mtime)
		}
		return s.upsertMovie(lib, path, rel, ext, info.Size(), mtime)
	}); err != nil {
		return err
	}
	return s.Store.CleanupLibrary(lib.ID, s.scanID)
}

func (s Scanner) upsertMovie(lib store.Library, path, rel, ext string, size, mtime int64) error {
	raw := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	ids := providerIDs(raw)
	name, year := metadata.CleanYear(cleanPart(cleanProviders(raw)))
	it := store.Item{ID: store.StableID("item", path), LibraryID: lib.ID, ParentID: lib.ID, Type: "Movie", Name: name, Path: path, RelativePath: rel, Container: ext, Size: size, MTimeUnix: mtime, ProductionYear: year, LastSeenScan: s.scanID}
	md, hasMeta := s.movieMeta(name, year, ids)
	if hasMeta {
		applyMeta(&it, md)
	} else {
		s.markMetadataMiss(&it)
	}
	if err := s.Store.UpsertItem(it); err != nil {
		return err
	}
	if hasMeta {
		if err := s.savePeople(it.ID, md); err != nil {
			return err
		}
		s.saveRemoteImages(it.ID, md)
	}
	return s.saveSidecars(it.ID, path)
}

func (s Scanner) upsertEpisode(lib store.Library, path, rel, ext string, size, mtime int64) error {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	rawSeriesName, ids := episodeSeriesSource(parts, path)
	seriesName, year := metadata.CleanYear(cleanPart(cleanProviders(rawSeriesName)))
	seriesID := store.StableID("series", lib.ID, seriesName)
	season := 1
	episode := 0
	if m := epRe.FindStringSubmatch(filepath.Base(path)); len(m) == 3 {
		season, _ = strconv.Atoi(m[1])
		episode, _ = strconv.Atoi(m[2])
	}
	seasonName := "Season " + strconv.Itoa(season)
	seasonID := store.StableID("season", seriesID, seasonName)
	series := store.Item{ID: seriesID, LibraryID: lib.ID, ParentID: lib.ID, Type: "Series", Name: seriesName, IsFolder: true, ProductionYear: year, LastSeenScan: s.scanID}
	md, hasMeta := s.seriesMeta(seriesName, year, ids)
	if hasMeta {
		applyMeta(&series, md)
	} else {
		s.markMetadataMiss(&series)
	}
	if err := s.Store.UpsertItem(series); err != nil {
		return err
	}
	if hasMeta {
		if err := s.savePeople(series.ID, md); err != nil {
			return err
		}
		s.saveRemoteImages(series.ID, md)
	}
	if err := s.saveFolderSidecars(series.ID, filepath.Join(filepath.Clean(lib.Path), parts[0])); err != nil {
		return err
	}
	seasonItem := store.Item{ID: seasonID, LibraryID: lib.ID, ParentID: seriesID, Type: "Season", Name: seasonName, IsFolder: true, IndexNumber: season, LastSeenScan: s.scanID}
	var smd metadata.Result
	if hasMeta && md.TMDBID != 0 {
		if got, ok := s.seasonMeta(md.TMDBID, season); ok {
			smd = got
			applyMeta(&seasonItem, smd)
			seasonItem.IndexNumber = season
		}
	}
	if err := s.Store.UpsertItem(seasonItem); err != nil {
		return err
	}
	if len(smd.People) > 0 {
		if err := s.savePeople(seasonID, smd); err != nil {
			return err
		}
	}
	if smd.PosterURL != "" {
		s.saveRemoteImages(seasonID, smd)
	}
	it := store.Item{ID: store.StableID("item", path), LibraryID: lib.ID, ParentID: seasonID, Type: "Episode", Name: cleanName(path), Path: path, RelativePath: rel, Container: ext, Size: size, MTimeUnix: mtime, IndexNumber: episode, ParentIndexNumber: season, LastSeenScan: s.scanID}
	var emd metadata.Result
	if hasMeta && md.TMDBID != 0 && episode != 0 {
		if got, ok := s.Meta.Episode(md.TMDBID, season, episode); ok {
			emd = got
			applyMeta(&it, emd)
			it.IndexNumber = episode
			it.ParentIndexNumber = season
		}
	}
	if it.ProviderIDsJSON == "" {
		s.markMetadataMiss(&it)
	}
	if err := s.Store.UpsertItem(it); err != nil {
		return err
	}
	if len(emd.People) > 0 {
		if err := s.savePeople(it.ID, emd); err != nil {
			return err
		}
	}
	if emd.PosterURL != "" {
		s.saveRemoteImages(it.ID, emd)
	}
	return s.saveSidecars(it.ID, path)
}

// upsertTrack indexes one audio file as Artist -> Album -> Audio, the
// hierarchy Jellyfin clients browse. Placement comes from the embedded tags
// and falls back to the folder layout, matching how Jellyfin treats a music
// library: tags win, so compilations and multi-disc albums stay together even
// when the directory names disagree.
func (s Scanner) upsertTrack(lib store.Library, path, rel, ext string, size, mtime int64) error {
	tags, _ := audio.Read(path)
	folders := strings.Split(filepath.ToSlash(rel), "/")
	artistName := firstNonEmpty(tags.AlbumArtist, first(tags.Artists), folderAt(folders, 0), unknownArtist)
	albumName := firstNonEmpty(tags.Album, folderAt(folders, 1), unknownAlbum)
	artists := tags.Artists
	if len(artists) == 0 {
		artists = []string{artistName}
	}
	// Albums commonly tag only some tracks with a disc number. Treating an
	// absent one as disc 1 keeps a mixed album in a single running order
	// instead of interleaving the untagged tracks ahead of the rest.
	disc := max(tags.Disc, 1)

	artistID := store.StableID("artist", lib.ID, strings.ToLower(artistName))
	albumID := store.StableID("album", artistID, strings.ToLower(albumName))
	artist := store.Item{ID: artistID, LibraryID: lib.ID, ParentID: lib.ID, Type: "MusicArtist", Name: artistName, SortName: artistSortName(artistName), IsFolder: true, LastSeenScan: s.scanID}
	if err := s.Store.UpsertItem(artist); err != nil {
		return err
	}
	if len(folders) > 1 {
		if err := s.saveFolderSidecars(artistID, filepath.Join(filepath.Clean(lib.Path), folders[0])); err != nil {
			return err
		}
	}

	album := store.Item{ID: albumID, LibraryID: lib.ID, ParentID: artistID, Type: "MusicAlbum", Name: albumName, IsFolder: true,
		ProductionYear: tags.Year, Album: albumName, AlbumArtist: artistName, ArtistsJSON: store.JSON([]string{artistName}),
		GenresJSON: genreJSON(tags.Genre), LastSeenScan: s.scanID}
	if err := s.Store.UpsertItem(album); err != nil {
		return err
	}
	// The album folder holds the cover art. Jellyfin gives a file beside the
	// media precedence over anything embedded in the track, and only when
	// there is none does the track's own picture stand in.
	if err := s.saveFolderSidecars(albumID, filepath.Dir(path)); err != nil {
		return err
	}
	if err := s.saveEmbeddedArt(albumID, path); err != nil {
		return err
	}

	track := store.Item{ID: store.StableID("item", path), LibraryID: lib.ID, ParentID: albumID, Type: "Audio",
		Name: firstNonEmpty(tags.Title, cleanName(path)), Path: path, RelativePath: rel, Container: ext, Size: size, MTimeUnix: mtime,
		IndexNumber: tags.Track, ParentIndexNumber: disc, RuntimeTicks: tags.DurationTicks, ProductionYear: tags.Year,
		Album: albumName, AlbumArtist: artistName, ArtistsJSON: store.JSON(artists), GenresJSON: genreJSON(tags.Genre),
		HasLyrics: tags.Lyrics != "", LastSeenScan: s.scanID}
	if err := s.Store.UpsertItem(track); err != nil {
		return err
	}
	return s.saveSidecars(track.ID, path)
}

// touchTrackParents keeps the album and artist of an unchanged track in the
// current scan. Reading the stored parents avoids re-parsing the file's tags
// only to recompute IDs that cannot have moved.
func (s Scanner) touchTrackParents(id string) error {
	track, err := s.Store.Item(id)
	if err != nil {
		return err
	}
	album, err := s.Store.Item(track.ParentID)
	if err != nil {
		return err
	}
	if err := s.Store.TouchItem(album.ID, s.scanID); err != nil {
		return err
	}
	return s.Store.TouchItem(album.ParentID, s.scanID)
}

func (s Scanner) touchEpisodeParents(lib store.Library, rel string) error {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	rawSeriesName := parts[0]
	if len(parts) == 1 {
		rawSeriesName = seriesNameFromEpisodeFile(filepath.Base(rel))
	}
	seriesName, _ := metadata.CleanYear(cleanPart(cleanProviders(rawSeriesName)))
	seriesID := store.StableID("series", lib.ID, seriesName)
	season := 1
	if m := epRe.FindStringSubmatch(filepath.Base(rel)); len(m) == 3 {
		season, _ = strconv.Atoi(m[1])
	}
	if err := s.Store.TouchItem(seriesID, s.scanID); err != nil {
		return err
	}
	return s.Store.TouchItem(store.StableID("season", seriesID, "Season "+strconv.Itoa(season)), s.scanID)
}

func (s Scanner) movieMeta(name string, year int, ids map[string]string) (metadata.Result, bool) {
	if id, _ := strconv.Atoi(ids["tmdbid"]); id != 0 {
		return s.Meta.MovieByID(id)
	}
	if id := ids["imdbid"]; id != "" {
		return s.Meta.MovieByIMDB(id)
	}
	return s.Meta.Movie(name, year)
}

func (s Scanner) needsMetadata(id string) bool {
	if !s.Meta.Enabled || s.Meta.Key == "" {
		return false
	}
	it, err := s.Store.Item(id)
	return err != nil || it.ProviderIDsJSON == ""
}

func (s Scanner) markMetadataMiss(it *store.Item) {
	if !s.Meta.Enabled || s.Meta.Key == "" || it.ProviderIDsJSON != "" {
		return
	}
	if current, err := s.Store.Item(it.ID); err == nil && current.ProviderIDsJSON != "" {
		return
	}
	it.ProviderIDsJSON = metadataNoMatch
}

func (s Scanner) seriesMeta(name string, year int, ids map[string]string) (metadata.Result, bool) {
	key := name + strconv.Itoa(year) + ids["tmdbid"] + ids["imdbid"]
	if md, ok := s.series[key]; ok {
		return md, md.TMDBID != 0
	}
	var md metadata.Result
	var ok bool
	if id, _ := strconv.Atoi(ids["tmdbid"]); id != 0 {
		md, ok = s.Meta.SeriesByID(id)
	} else if id := ids["imdbid"]; id != "" {
		md, ok = s.Meta.SeriesByIMDB(id)
	} else if id := ids["tvdbid"]; id != "" {
		md, ok = s.Meta.SeriesByTVDB(id)
	} else {
		md, ok = s.Meta.Series(name, year)
	}
	if ok {
		s.series[key] = md
	}
	return md, ok
}

func (s Scanner) seasonMeta(seriesID, season int) (metadata.Result, bool) {
	key := strconv.Itoa(seriesID) + ":" + strconv.Itoa(season)
	if md, ok := s.seasons[key]; ok {
		return md, md.TMDBID != 0
	}
	md, ok := s.Meta.Season(seriesID, season)
	if ok {
		s.seasons[key] = md
	}
	return md, ok
}

func applyMeta(it *store.Item, md metadata.Result) {
	if md.Name != "" {
		it.Name = md.Name
	}
	if md.Overview != "" {
		it.Overview = md.Overview
	}
	if md.PremiereDate != "" {
		it.PremiereDate = md.PremiereDate
	}
	if md.Year != 0 {
		it.ProductionYear = md.Year
	}
	it.ProviderIDsJSON = metadata.ProviderIDs(md.TMDBID, md.IMDBID)
	it.GenresJSON = store.JSON(md.Genres)
	it.StudiosJSON = store.JSON(md.Studios)
	it.PeopleJSON = store.JSON(md.People)
	it.TaglinesJSON = store.JSON(md.Taglines)
	it.ExternalURLsJSON = store.JSON(md.ExternalURLs)
	it.CommunityRating = md.CommunityRating
	it.OfficialRating = md.OfficialRating
	it.RuntimeTicks = md.RuntimeTicks
}

func (s Scanner) saveRemoteImages(id string, md metadata.Result) {
	if md.PosterURL != "" {
		_ = s.Store.UpsertImage(store.Image{ItemID: id, Type: "Primary", Path: md.PosterURL, Tag: store.StableID(md.PosterURL), Mime: "image/jpeg"})
	}
	if md.BackdropURL != "" {
		_ = s.Store.UpsertImage(store.Image{ItemID: id, Type: "Backdrop", Index: 0, Path: md.BackdropURL, Tag: store.StableID(md.BackdropURL), Mime: "image/jpeg"})
	}
}

func (s Scanner) savePeople(id string, md metadata.Result) error {
	people := make([]store.Person, 0, len(md.People))
	for i, p := range md.People {
		people = append(people, store.Person{TMDBID: p.TMDBID, Name: p.Name, Role: p.Type, Character: p.Role, ProfileURL: p.ProfileURL, SortOrder: i})
	}
	return s.Store.SavePeople(id, people)
}

func (s Scanner) saveSidecars(id, video string) error {
	base := strings.TrimSuffix(video, filepath.Ext(video))
	for _, name := range []string{base + ".jpg", base + ".png", filepath.Join(filepath.Dir(video), "poster.jpg"), filepath.Join(filepath.Dir(video), "folder.jpg")} {
		if mime := imageExt[store.Ext(name)]; mime != "" && exists(name) {
			return s.Store.UpsertImage(store.Image{ItemID: id, Type: "Primary", Path: name, Tag: store.StableID(name), Mime: mime})
		}
	}
	return nil
}

// saveEmbeddedArt falls back to the cover inside a track when the album folder
// holds no image file. The art is copied out once per album and cached beside
// the database, so serving it later needs no audio parsing and reuses the same
// file handling as a sidecar image. Art is optional, so a failure to extract or
// write one never fails the scan.
func (s Scanner) saveEmbeddedArt(albumID, path string) error {
	if s.CoverDir == "" || s.covers[albumID] {
		return nil
	}
	s.covers[albumID] = true
	imgs, err := s.Store.Images(albumID)
	if err != nil || len(imgs) > 0 {
		return err
	}
	data, mime, err := audio.Picture(path)
	if err != nil {
		return nil
	}
	if err := os.MkdirAll(s.CoverDir, 0700); err != nil {
		return nil
	}
	file := filepath.Join(s.CoverDir, albumID+audio.PictureExt(mime))
	if err := writeCover(file, data); err != nil {
		return nil
	}
	return s.Store.UpsertImage(store.Image{ItemID: albumID, Type: "Primary", Path: file,
		Tag: store.StableID("cover", albumID, strconv.Itoa(len(data))), Mime: mime})
}

// writeCover replaces a cover atomically, so an interrupted scan cannot leave a
// half-written image behind to be served as the album's art.
func writeCover(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// pruneCovers deletes cached art whose album is gone, so the cache cannot grow
// without bound as a library changes. Each file is named for its album, and a
// leftover temporary file matches no album either.
func (s Scanner) pruneCovers() error {
	if s.CoverDir == "" {
		return nil
	}
	entries, err := os.ReadDir(s.CoverDir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		name := entry.Name()
		id := strings.TrimSuffix(name, filepath.Ext(name))
		if _, err := s.Store.Item(id); err == nil {
			continue
		}
		_ = os.Remove(filepath.Join(s.CoverDir, name))
	}
	return nil
}

func (s Scanner) saveFolderSidecars(id, dir string) error {
	for _, name := range []string{"poster.jpg", "folder.jpg", "cover.jpg"} {
		p := filepath.Join(dir, name)
		if mime := imageExt[store.Ext(p)]; mime != "" && exists(p) {
			return s.Store.UpsertImage(store.Image{ItemID: id, Type: "Primary", Path: p, Tag: store.StableID(p), Mime: mime})
		}
	}
	return nil
}

func cleanName(path string) string {
	return cleanPart(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
}

// Names used when a track carries no tags and sits directly in the library
// root, so there is no folder to borrow a name from either.
const (
	unknownArtist = "Unknown Artist"
	unknownAlbum  = "Unknown Album"
)

// artistSortName files an artist under its first significant word, so that
// "The Beatles" sorts under B the way a record shelf does. Items store sort
// names lowercased.
func artistSortName(name string) string {
	lower := strings.ToLower(name)
	if rest, ok := strings.CutPrefix(lower, "the "); ok && rest != "" {
		return rest
	}
	return lower
}

// folderAt returns the folder at depth i of a path relative to the library
// root, or "" when the file sits above that depth.
func folderAt(parts []string, i int) string {
	if i >= len(parts)-1 {
		return ""
	}
	return cleanPart(parts[i])
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// genreJSON encodes a single tag genre the way TMDB genre lists are stored, so
// music and video items expose the same Genres field.
func genreJSON(genre string) string {
	if genre == "" {
		return ""
	}
	return store.JSON([]string{genre})
}

func seriesNameFromEpisodeFile(name string) string {
	if loc := epRe.FindStringIndex(name); loc != nil {
		name = name[:loc[0]]
	}
	return strings.TrimSpace(name)
}

func episodeSeriesSource(parts []string, path string) (string, map[string]string) {
	if len(parts) == 1 {
		// Also accept a flat library such as "TV/Show Name S01E01.mkv".
		// The recommended layout remains one show directory per series.
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		return seriesNameFromEpisodeFile(name), providerIDs(name)
	}
	return parts[0], providerIDs(parts[0])
}

func cleanPart(s string) string {
	s = strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}
func cleanProviders(s string) string {
	return strings.Join(strings.Fields(providerRe.ReplaceAllString(s, "")), " ")
}
func providerIDs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range providerRe.FindAllStringSubmatch(s, -1) {
		out[strings.ToLower(m[1])] = strings.TrimSpace(m[2])
	}
	return out
}
func exists(p string) bool { _, err := os.Stat(p); return err == nil }
