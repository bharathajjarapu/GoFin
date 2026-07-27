package audio

import (
	"strconv"
	"strings"
)

// maxLyricFraction is the number of fractional-second digits an LRC timestamp
// is read to. Anything finer than a millisecond is noise for a scrolling view.
const maxLyricFraction = 3

// Line is one line of lyrics. Timed reports whether the file positioned it,
// which is what lets a client scroll the words in step with playback.
type Line struct {
	Text  string
	Start int64
	Timed bool
}

// ParseLyrics splits embedded lyric text into lines, reading the "[mm:ss.xx]"
// timestamps of the LRC format where they are present. Text without them still
// yields lines, just unpositioned, which clients display as a static sheet.
func ParseLyrics(text string) []Line {
	var out []Line
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if line, ok := parseLyricLine(raw); ok {
			out = append(out, line)
		}
	}
	return out
}

// parseLyricLine strips the leading timestamps from one line, keeping the first
// as its position. It rejects a line whose brackets hold something other than a
// timestamp, which is how an LRC file writes its "[ar:...]" headers, and a line
// that ends up carrying neither a time nor any words.
func parseLyricLine(raw string) (Line, bool) {
	var line Line
	rest := strings.TrimSpace(raw)
	for strings.HasPrefix(rest, "[") {
		end := strings.Index(rest, "]")
		if end < 0 {
			break
		}
		ticks, ok := lyricTicks(rest[1:end])
		if !ok {
			return Line{}, false
		}
		if !line.Timed {
			line.Start, line.Timed = ticks, true
		}
		rest = strings.TrimSpace(rest[end+1:])
	}
	line.Text = rest
	return line, line.Timed || line.Text != ""
}

// lyricTicks converts an "mm:ss.xx" timestamp to Jellyfin ticks.
func lyricTicks(s string) (int64, bool) {
	minutes, rest, ok := strings.Cut(s, ":")
	if !ok {
		return 0, false
	}
	seconds, fraction, _ := strings.Cut(rest, ".")
	m, errMinutes := strconv.Atoi(strings.TrimSpace(minutes))
	sec, errSeconds := strconv.Atoi(strings.TrimSpace(seconds))
	if errMinutes != nil || errSeconds != nil || m < 0 || sec < 0 || sec > 59 {
		return 0, false
	}
	ticks := int64(m*60+sec) * TicksPerSecond
	if fraction == "" {
		return ticks, true
	}
	if len(fraction) > maxLyricFraction {
		fraction = fraction[:maxLyricFraction]
	}
	value, err := strconv.Atoi(fraction)
	if err != nil || value < 0 {
		return 0, false
	}
	// Two digits mean hundredths, three mean thousandths.
	scale := int64(TicksPerSecond)
	for range len(fraction) {
		scale /= 10
	}
	return ticks + int64(value)*scale, true
}
