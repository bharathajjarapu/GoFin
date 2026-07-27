package audio

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// vorbisComments builds a comment payload: a vendor string then a list of
// length-prefixed "KEY=value" entries.
func vorbisComments(entries ...string) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, uint32(0))
	binary.Write(&b, binary.LittleEndian, uint32(len(entries)))
	for _, e := range entries {
		binary.Write(&b, binary.LittleEndian, uint32(len(e)))
		b.WriteString(e)
	}
	return b.Bytes()
}

// flacFile builds a FLAC stream with a STREAMINFO block reporting the given
// sample count and rate, followed by a comment block.
func flacFile(samples, rate uint64, entries ...string) []byte {
	var b bytes.Buffer
	b.WriteString("fLaC")

	info := make([]byte, 34)
	binary.BigEndian.PutUint64(info[10:18], rate<<44|samples&0xF_FFFF_FFFF)
	b.WriteByte(blockStreamInfo)
	b.Write([]byte{0, 0, byte(len(info))})
	b.Write(info)

	comments := vorbisComments(entries...)
	b.WriteByte(blockComment | 0x80)
	b.Write([]byte{byte(len(comments) >> 16), byte(len(comments) >> 8), byte(len(comments))})
	b.Write(comments)
	return b.Bytes()
}

// oggPage wraps payload in a single Ogg page carrying the given granule.
func oggPage(granule uint64, payload []byte) []byte {
	var b bytes.Buffer
	b.WriteString("OggS")
	b.Write([]byte{0, 0})
	binary.Write(&b, binary.LittleEndian, granule)
	b.Write(make([]byte, 12))
	var segments []byte
	for n := len(payload); ; n -= 255 {
		if n < 255 {
			segments = append(segments, byte(n))
			break
		}
		segments = append(segments, 255)
	}
	b.WriteByte(byte(len(segments)))
	b.Write(segments)
	b.Write(payload)
	return b.Bytes()
}

func opusFile(granule uint64, entries ...string) []byte {
	head := append([]byte("OpusHead"), make([]byte, 11)...)
	binary.LittleEndian.PutUint16(head[10:12], 312) // pre-skip
	tags := append([]byte("OpusTags"), vorbisComments(entries...)...)
	return append(oggPage(0, head), oggPage(granule, tags)...)
}

func vorbisFile(granule uint64, rate uint32, entries ...string) []byte {
	id := make([]byte, 30)
	id[0] = 1
	copy(id[1:], "vorbis")
	binary.LittleEndian.PutUint32(id[12:16], rate)
	comment := append(append([]byte{3}, "vorbis"...), vorbisComments(entries...)...)
	return append(oggPage(0, id), oggPage(granule, comment)...)
}

// atom builds an MP4 atom from a name and payload.
func atom(name string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(len(body)+atomHeader))
	return append(append(b, name...), body...)
}

func tagAtom(name, value string) []byte {
	return atom(name, atom("data", make([]byte, 8), []byte(value)))
}

func mp4File(duration, timescale uint32, title, album, artist string) []byte {
	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:16], timescale)
	binary.BigEndian.PutUint32(mvhd[16:20], duration)
	ilst := atom("ilst", tagAtom("\xa9nam", title), tagAtom("\xa9alb", album), tagAtom("aART", artist), atom("trkn", atom("data", make([]byte, 8), []byte{0, 0, 0, 7})))
	// meta carries a version and flags word ahead of its children.
	meta := atom("meta", make([]byte, 4), ilst)
	return atom("moov", atom("mvhd", mvhd), atom("udta", meta))
}

func write(t *testing.T, name string, b []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadFLAC(t *testing.T) {
	path := write(t, "track.flac", flacFile(441000, 44100,
		"TITLE=Blue Monday", "ALBUM=Power", "ALBUMARTIST=New Order",
		"ARTIST=New Order", "TRACKNUMBER=3/8", "DISCNUMBER=1", "DATE=1983-03-07", "GENRE=Post-Punk"))
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Tags{Title: "Blue Monday", Album: "Power", AlbumArtist: "New Order", Genre: "Post-Punk",
		Artists: []string{"New Order"}, Track: 3, Disc: 1, Year: 1983, DurationTicks: 10 * TicksPerSecond}
	if got.Title != want.Title || got.Album != want.Album || got.AlbumArtist != want.AlbumArtist ||
		got.Genre != want.Genre || got.Track != want.Track || got.Disc != want.Disc ||
		got.Year != want.Year || got.DurationTicks != want.DurationTicks || len(got.Artists) != 1 {
		t.Fatalf("Read() = %#v, want %#v", got, want)
	}
}

func TestReadOpus(t *testing.T) {
	// 48312 granules less the 312 pre-skip is one second at 48 kHz.
	path := write(t, "track.opus", opusFile(48312, "TITLE=Song", "ARTIST=A", "ARTIST=B"))
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Song" || got.DurationTicks != TicksPerSecond {
		t.Fatalf("Read() = %#v", got)
	}
	if len(got.Artists) != 2 || got.Artists[1] != "B" {
		t.Fatalf("artists = %#v", got.Artists)
	}
}

func TestReadVorbis(t *testing.T) {
	path := write(t, "track.ogg", vorbisFile(88200, 44100, "TITLE=Song", "ALBUM=Record"))
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Song" || got.Album != "Record" || got.DurationTicks != 2*TicksPerSecond {
		t.Fatalf("Read() = %#v", got)
	}
}

func TestReadMP4(t *testing.T) {
	path := write(t, "track.m4a", mp4File(90000, 45000, "Song", "Record", "Artist"))
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Song" || got.Album != "Record" || got.AlbumArtist != "Artist" ||
		got.Track != 7 || got.DurationTicks != 2*TicksPerSecond {
		t.Fatalf("Read() = %#v", got)
	}
}

func TestReadRejectsUnusableFiles(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{name: "track.flac", body: []byte("not a flac file")},
		{name: "track.ogg", body: []byte("not an ogg file at all")},
		{name: "track.m4a", body: []byte("not an mp4 file")},
		{name: "track.mp3", body: []byte("unsupported container")},
		{name: "track.flac", body: nil},
	}
	for _, test := range tests {
		if _, err := Read(write(t, test.name, test.body)); err == nil {
			t.Fatalf("Read(%s) succeeded on %q", test.name, test.body)
		}
	}
}

// Truncated and oversized headers must stop the walk rather than panic or
// allocate on the numbers a corrupt file claims.
func TestReadSurvivesCorruptHeaders(t *testing.T) {
	full := flacFile(441000, 44100, "TITLE=Song", "ALBUM=Record")
	for cut := len(full) - 1; cut > 4; cut-- {
		if _, err := Read(write(t, "track.flac", full[:cut])); err != nil {
			t.Fatalf("truncating to %d bytes failed: %v", cut, err)
		}
	}
	var claims Tags
	parseVorbisComments([]byte{0xFF, 0xFF, 0xFF, 0xFF}, &claims)
	parseVorbisComments(append(vorbisComments("TITLE=Song"), 0xFF), &claims)
	if claims.Title != "Song" {
		t.Fatalf("tags = %#v", claims)
	}
}

func TestDurationTicksRejectsImpossibleLengths(t *testing.T) {
	if got := durationTicks(1<<62, 1); got != 0 {
		t.Fatalf("durationTicks() = %d, want 0 for an impossible length", got)
	}
	if got := durationTicks(100, 0); got != 0 {
		t.Fatalf("durationTicks() = %d, want 0 for a zero rate", got)
	}
	if got := durationTicks(44100, 44100); got != TicksPerSecond {
		t.Fatalf("durationTicks() = %d, want one second", got)
	}
}
