package library

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gofin/internal/config"
	"gofin/internal/metadata"
	"gofin/internal/store"
)

type Scanner struct {
	Store   *store.Store
	Meta    metadata.Client
	series  map[string]metadata.Result
	seasons map[string]metadata.Result
}

var videoExt = map[string]bool{"mkv": true, "mp4": true, "m4v": true, "avi": true, "mov": true, "webm": true}
var imageExt = map[string]string{"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp"}
var epRe = regexp.MustCompile(`(?i)s(\d{1,2})e(\d{1,3})`)

func (s Scanner) Scan(libs []config.Library, only string) error {
	if s.series == nil {
		s.series = map[string]metadata.Result{}
		s.seasons = map[string]metadata.Result{}
	}
	var slibs []store.Library
	for _, l := range libs {
		if info, err := os.Stat(l.Path); err != nil || !info.IsDir() {
			continue
		}
		slibs = append(slibs, store.Library{ID: store.StableID("library", l.Path), Name: l.Name, Type: l.Type, Path: l.Path})
	}
	if err := s.Store.SaveLibraries(slibs); err != nil {
		return err
	}
	for _, l := range slibs {
		if only != "" && only != l.ID && !strings.EqualFold(only, l.Name) {
			continue
		}
		if err := s.scanLibrary(l); err != nil {
			return err
		}
	}
	return nil
}

func (s Scanner) scanLibrary(lib store.Library) error {
	root := filepath.Clean(lib.Path)
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := store.Ext(path)
		if !videoExt[ext] {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if lib.Type == "tvshows" {
			return s.upsertEpisode(lib, path, rel, ext, info.Size())
		}
		name, year := metadata.CleanYear(cleanName(path))
		it := store.Item{ID: store.StableID("item", path), LibraryID: lib.ID, ParentID: lib.ID, Type: "Movie", Name: name, Path: path, RelativePath: rel, Container: ext, Size: info.Size(), ProductionYear: year}
		var md metadata.Result
		hasMeta := false
		if md, hasMeta = s.Meta.Movie(name, year); hasMeta {
			applyMeta(&it, md)
		}
		if err := s.Store.UpsertItem(it); err != nil {
			return err
		}
		if hasMeta {
			s.saveRemoteImages(it.ID, md)
		}
		return s.saveSidecars(it.ID, path)
	})
}

func (s Scanner) upsertEpisode(lib store.Library, path, rel, ext string, size int64) error {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	seriesName, year := metadata.CleanYear(cleanPart(parts[0]))
	seriesID := store.StableID("series", lib.ID, seriesName)
	season := 1
	episode := 0
	if m := epRe.FindStringSubmatch(filepath.Base(path)); len(m) == 3 {
		season = atoi(m[1])
		episode = atoi(m[2])
	}
	seasonName := "Season " + itoa(season)
	seasonID := store.StableID("season", seriesID, seasonName)
	series := store.Item{ID: seriesID, LibraryID: lib.ID, ParentID: lib.ID, Type: "Series", Name: seriesName, IsFolder: true, ProductionYear: year}
	md, hasMeta := s.seriesMeta(seriesName, year)
	if hasMeta {
		applyMeta(&series, md)
	}
	if err := s.Store.UpsertItem(series); err != nil {
		return err
	}
	if hasMeta {
		s.saveRemoteImages(series.ID, md)
	}
	if err := s.saveFolderSidecars(series.ID, filepath.Join(filepath.Clean(lib.Path), parts[0])); err != nil {
		return err
	}
	seasonItem := store.Item{ID: seasonID, LibraryID: lib.ID, ParentID: seriesID, Type: "Season", Name: seasonName, IsFolder: true, IndexNumber: season}
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
	if smd.PosterURL != "" {
		s.saveRemoteImages(seasonID, smd)
	}
	it := store.Item{ID: store.StableID("item", path), LibraryID: lib.ID, ParentID: seasonID, Type: "Episode", Name: cleanName(path), Path: path, RelativePath: rel, Container: ext, Size: size, IndexNumber: episode, ParentIndexNumber: season}
	var emd metadata.Result
	if hasMeta && md.TMDBID != 0 && episode != 0 {
		if got, ok := s.Meta.Episode(md.TMDBID, season, episode); ok {
			emd = got
			applyMeta(&it, emd)
			it.IndexNumber = episode
			it.ParentIndexNumber = season
		}
	}
	if err := s.Store.UpsertItem(it); err != nil {
		return err
	}
	if emd.PosterURL != "" {
		s.saveRemoteImages(it.ID, emd)
	}
	return s.saveSidecars(it.ID, path)
}

func (s Scanner) seriesMeta(name string, year int) (metadata.Result, bool) {
	key := name + itoa(year)
	if md, ok := s.series[key]; ok {
		return md, md.TMDBID != 0
	}
	md, ok := s.Meta.Series(name, year)
	if ok {
		s.series[key] = md
	}
	return md, ok
}

func (s Scanner) seasonMeta(seriesID, season int) (metadata.Result, bool) {
	key := itoa(seriesID) + ":" + itoa(season)
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
	it.ProviderIDsJSON = metadata.ProviderIDs(md.TMDBID)
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

func (s Scanner) saveSidecars(id, video string) error {
	base := strings.TrimSuffix(video, filepath.Ext(video))
	for _, name := range []string{base + ".jpg", base + ".png", filepath.Join(filepath.Dir(video), "poster.jpg"), filepath.Join(filepath.Dir(video), "folder.jpg")} {
		if mime := imageExt[store.Ext(name)]; mime != "" && exists(name) {
			return s.Store.UpsertImage(store.Image{ItemID: id, Type: "Primary", Path: name, Tag: store.StableID(name), Mime: mime})
		}
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
func cleanPart(s string) string {
	s = strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}
func exists(p string) bool { _, err := os.Stat(p); return err == nil }
func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
