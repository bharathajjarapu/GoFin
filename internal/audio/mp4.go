package audio

import (
	"encoding/binary"
	"io"
)

const (
	// atomHeader is the size and type prefix every atom carries.
	atomHeader = 8
	// maxAtomPayload bounds one metadata value read from an untrusted file.
	maxAtomPayload = 1 << 20
	// maxAtomDepth stops a crafted file from nesting containers without end.
	maxAtomDepth = 8
	// dataAtomPrefix covers the header, version and locale words that precede
	// the value inside an iTunes "data" atom.
	dataAtomPrefix = 16
)

// containerAtoms hold further atoms on the path to the movie header and the
// iTunes metadata list.
var containerAtoms = map[string]bool{"moov": true, "udta": true}

// readMP4 walks the atom tree of an MP4 container, which is what M4A files
// holding AAC or ALAC are. The movie header gives the duration and the iTunes
// metadata list gives the tags.
func readMP4(r io.ReadSeeker, size int64) (Tags, error) {
	var t Tags
	walkAtoms(r, 0, size, &t, 0)
	if t.DurationTicks == 0 && t.Title == "" && t.Album == "" {
		return t, ErrUnsupported
	}
	return t, nil
}

// walkAtoms iterates the atoms between start and end, descending into the ones
// that lead to metadata and skipping everything else.
func walkAtoms(r io.ReadSeeker, start, end int64, t *Tags, depth int) {
	if depth > maxAtomDepth {
		return
	}
	for off := start; off+atomHeader <= end; {
		size, name, body, ok := readAtomHeader(r, off, end)
		if !ok {
			return
		}
		next := off + size
		switch {
		case name == "mvhd":
			readMovieHeader(r, body, next, t)
		case name == "meta":
			// meta carries a version and flags word ahead of its children.
			walkAtoms(r, body+4, next, t, depth+1)
		case name == "ilst":
			readTagList(r, body, next, t)
		case containerAtoms[name]:
			walkAtoms(r, body, next, t, depth+1)
		}
		off = next
	}
}

// readAtomHeader reads one atom header, resolving both extended-size forms.
// It returns the atom's total size, type, and the offset its payload starts at.
func readAtomHeader(r io.ReadSeeker, off, end int64) (size int64, name string, body int64, ok bool) {
	var head [atomHeader]byte
	if _, err := r.Seek(off, io.SeekStart); err != nil {
		return 0, "", 0, false
	}
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, "", 0, false
	}
	size, name, body = int64(binary.BigEndian.Uint32(head[:4])), string(head[4:]), off+atomHeader
	switch size {
	case 1:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return 0, "", 0, false
		}
		size, body = int64(binary.BigEndian.Uint64(ext[:])), body+8
	case 0:
		size = end - off
	}
	if size < body-off || off+size > end {
		return 0, "", 0, false
	}
	return size, name, body, true
}

// readMovieHeader reads the timescale and duration from mvhd. Version 1 widens
// both timestamps and the duration to 64 bits.
func readMovieHeader(r io.ReadSeeker, start, end int64, t *Tags) {
	b, ok := readAt(r, start, end, 32)
	if !ok {
		return
	}
	switch {
	case b[0] == 0 && len(b) >= 20:
		t.DurationTicks = durationTicks(uint64(binary.BigEndian.Uint32(b[16:20])), uint64(binary.BigEndian.Uint32(b[12:16])))
	case b[0] == 1 && len(b) >= 32:
		t.DurationTicks = durationTicks(binary.BigEndian.Uint64(b[24:32]), uint64(binary.BigEndian.Uint32(b[20:24])))
	}
}

// readTagList reads the iTunes metadata atoms. Each child names one tag and
// wraps its value in a "data" atom.
func readTagList(r io.ReadSeeker, start, end int64, t *Tags) {
	for off := start; off+atomHeader <= end; {
		size, name, body, ok := readAtomHeader(r, off, end)
		if !ok {
			return
		}
		if value, ok := readDataAtom(r, body, off+size); ok {
			applyMP4Tag(t, name, value)
		}
		off += size
	}
}

// readDataAtom returns the value held by the "data" atom of an iTunes tag.
func readDataAtom(r io.ReadSeeker, start, end int64) ([]byte, bool) {
	for off := start; off+atomHeader <= end; {
		size, name, _, ok := readAtomHeader(r, off, end)
		if !ok {
			return nil, false
		}
		if name == "data" {
			return readAt(r, off+dataAtomPrefix, off+size, maxAtomPayload)
		}
		off += size
	}
	return nil, false
}

// readAt reads the bytes between start and end, up to limit.
func readAt(r io.ReadSeeker, start, end, limit int64) ([]byte, bool) {
	n := min(end-start, limit)
	if n <= 0 {
		return nil, false
	}
	if _, err := r.Seek(start, io.SeekStart); err != nil {
		return nil, false
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, false
	}
	return b, true
}

// applyMP4Tag records one iTunes tag. The names beginning "\xa9" are the
// copyright-sign atoms the format uses for free text.
func applyMP4Tag(t *Tags, name string, v []byte) {
	switch name {
	case "\xa9nam":
		t.Title = firstNonEmpty(t.Title, string(v))
	case "\xa9alb":
		t.Album = firstNonEmpty(t.Album, string(v))
	case "aART":
		t.AlbumArtist = firstNonEmpty(t.AlbumArtist, string(v))
	case "\xa9ART":
		t.Artists = append(t.Artists, splitArtists(string(v))...)
	case "\xa9gen":
		t.Genre = firstNonEmpty(t.Genre, string(v))
	case "\xa9day":
		if t.Year == 0 {
			t.Year = leadingInt(string(v))
		}
	case "trkn":
		if len(v) >= 4 {
			t.Track = int(binary.BigEndian.Uint16(v[2:4]))
		}
	case "disk":
		if len(v) >= 4 {
			t.Disc = int(binary.BigEndian.Uint16(v[2:4]))
		}
	}
}
