package audio

import (
	"encoding/binary"
	"strings"
)

// maxComments bounds the comment list of an untrusted file.
const maxComments = 512

// parseVorbisComments reads the comment payload shared by FLAC, Ogg Vorbis and
// Opus: a vendor string followed by a list of "KEY=value" entries, every field
// length-prefixed little-endian. Malformed input stops the walk and keeps the
// tags gathered so far.
func parseVorbisComments(b []byte, t *Tags) {
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
		applyVorbisTag(t, string(b[off:off+size]))
		off += size
	}
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
	}
}
