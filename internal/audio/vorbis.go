package audio

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// maxComments bounds the comment list of an untrusted file.
const maxComments = 512

// eachVorbisComment walks the comment payload shared by FLAC, Ogg Vorbis and
// Opus: a vendor string followed by a list of "KEY=value" entries, every field
// length-prefixed little-endian. Malformed or truncated input stops the walk,
// keeping whatever was gathered so far. visit stops the walk by returning false.
func eachVorbisComment(b []byte, visit func(entry []byte) bool) {
	if len(b) < 4 {
		return
	}
	total := uint64(len(b))
	off := 4 + uint64(binary.LittleEndian.Uint32(b))
	if off+4 > total {
		return
	}
	count := uint64(binary.LittleEndian.Uint32(b[off:]))
	off += 4
	if count > maxComments {
		count = maxComments
	}
	for range count {
		if off+4 > total {
			return
		}
		size := uint64(binary.LittleEndian.Uint32(b[off:]))
		off += 4
		if off+size > total {
			return
		}
		if !visit(b[off : off+size]) {
			return
		}
		off += size
	}
}

// parseVorbisComments records every tag in a comment payload.
func parseVorbisComments(b []byte, t *Tags) {
	eachVorbisComment(b, func(entry []byte) bool {
		applyVorbisTag(t, string(entry))
		return true
	})
}

// findVorbisComment returns the value of one comment by key, without building a
// string for any of the others. An embedded cover arrives this way and is
// thousands of times larger than a tag.
func findVorbisComment(b []byte, key string) (string, bool) {
	var out string
	found := false
	eachVorbisComment(b, func(entry []byte) bool {
		name, value, ok := bytes.Cut(entry, []byte("="))
		if ok && strings.EqualFold(string(name), key) {
			out, found = string(value), true
		}
		return !found
	})
	return out, found
}

// applyVorbisTag records one "KEY=value" comment. Repeated ARTIST entries
// accumulate because a track may credit several performers; every other field
// keeps the first value seen.
func applyVorbisTag(t *Tags, entry string) {
	key, value, ok := strings.Cut(entry, "=")
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return
	}
	switch strings.ToUpper(key) {
	case "TITLE":
		t.Title = firstNonEmpty(t.Title, value)
	case "ALBUM":
		t.Album = firstNonEmpty(t.Album, value)
	case "ALBUMARTIST", "ALBUM ARTIST":
		t.AlbumArtist = firstNonEmpty(t.AlbumArtist, value)
	case "ARTIST":
		t.Artists = append(t.Artists, splitArtists(value)...)
	case "GENRE":
		t.Genre = firstNonEmpty(t.Genre, value)
	case "TRACKNUMBER":
		if t.Track == 0 {
			t.Track = leadingInt(value)
		}
	case "DISCNUMBER":
		if t.Disc == 0 {
			t.Disc = leadingInt(value)
		}
	case "DATE", "YEAR":
		if t.Year == 0 {
			t.Year = leadingInt(value)
		}
	case "LYRICS", "UNSYNCEDLYRICS", "SYNCEDLYRICS":
		t.Lyrics = firstNonEmpty(t.Lyrics, value)
	}
}
