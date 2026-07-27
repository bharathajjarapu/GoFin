// Package audio reads tags and durations from audio files by parsing only
// container headers. Nothing is decoded, so scanning stays cheap and the
// server needs neither ffmpeg nor cgo.
package audio

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// TicksPerSecond matches the 100-nanosecond unit Jellyfin reports durations in.
const TicksPerSecond = 10_000_000

// maxDurationSeconds bounds a parsed duration. Headers come from files we do
// not control, and rejecting impossible values keeps the tick maths in range.
const maxDurationSeconds = 24 * 60 * 60

// ErrUnsupported reports a file this package cannot parse.
var ErrUnsupported = errors.New("unsupported audio container")

// containers lists the extensions GoFin reads. Every one of them is
// direct-play friendly, which is the point: the server never transcodes.
var containers = map[string]bool{"flac": true, "m4a": true, "m4b": true, "ogg": true, "oga": true, "opus": true}

// Tags holds the metadata a scan needs. Absent values stay zero so callers can
// fall back to file and folder names.
type Tags struct {
	Title, Album, AlbumArtist, Genre string
	Lyrics                           string
	Artists                          []string
	Track, Disc, Year                int
	DurationTicks                    int64
}

// Supported reports whether Read can parse files with this extension.
func Supported(ext string) bool { return containers[ext] }

// Ext returns the lowercase extension of path without its dot.
func Ext(path string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
}

// Read parses the tags and duration of an audio file. A file with no usable
// tags reads back as a zero Tags rather than an error, because the scanner can
// still place it using its path.
func Read(path string) (Tags, error) {
	f, err := os.Open(path)
	if err != nil {
		return Tags{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Tags{}, err
	}
	switch Ext(path) {
	case "flac":
		return readFLAC(f)
	case "ogg", "oga", "opus":
		return readOgg(f, info.Size())
	case "m4a", "m4b":
		return readMP4(f, info.Size())
	}
	return Tags{}, ErrUnsupported
}

// durationTicks converts a sample or time-unit count to Jellyfin ticks. It
// divides before multiplying and rejects impossible lengths so that a corrupt
// header cannot overflow the result.
func durationTicks(count, rate uint64) int64 {
	if rate == 0 || count == 0 || count/rate > maxDurationSeconds {
		return 0
	}
	return int64(count/rate*TicksPerSecond + count%rate*TicksPerSecond/rate)
}

// leadingInt reads the number at the start of s, which tolerates the "5/12"
// track numbering and "1994-08-30" dates that tags commonly carry.
func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}

// splitArtists breaks one artist tag into the performers it credits. Semicolon
// is the conventional separator; a slash is deliberately not treated as one,
// because it appears inside names such as AC/DC. A file that credits several
// artists properly repeats the tag instead, which callers already accumulate.
func splitArtists(value string) []string {
	parts := strings.Split(value, ";")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// firstNonEmpty returns the first value that is not blank.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
