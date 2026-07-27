package audio

import (
	"bytes"
	"encoding/base64"
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

func be24(n int) []byte { return []byte{byte(n >> 16), byte(n >> 8), byte(n)} }

// pictureBlock builds the FLAC PICTURE layout, which Ogg carries base64 encoded
// inside a comment.
func pictureBlock(mime string, data []byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(3)) // front cover
	binary.Write(&b, binary.BigEndian, uint32(len(mime)))
	b.WriteString(mime)
	binary.Write(&b, binary.BigEndian, uint32(0)) // empty description
	b.Write(make([]byte, pictureHeader))          // width, height, depth, colours
	binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.Write(data)
	return b.Bytes()
}

// flacWithPicture puts a PICTURE block between STREAMINFO and the comments, so
// the walk has to step over it to reach the tags.
func flacWithPicture(picture []byte, entries ...string) []byte {
	var b bytes.Buffer
	b.WriteString("fLaC")
	info := make([]byte, 34)
	binary.BigEndian.PutUint64(info[10:18], 44100<<44|44100)
	b.WriteByte(blockStreamInfo)
	b.Write(be24(len(info)))
	b.Write(info)
	b.WriteByte(blockPicture)
	b.Write(be24(len(picture)))
	b.Write(picture)
	comments := vorbisComments(entries...)
	b.WriteByte(blockComment | 0x80)
	b.Write(be24(len(comments)))
	b.Write(comments)
	return b.Bytes()
}

// mp4WithCover builds an M4A carrying a covr atom. Its data atom's type flag
// names the image format: 13 is JPEG.
func mp4WithCover(mime byte, art []byte, entries ...[]byte) []byte {
	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:16], 1000)
	binary.BigEndian.PutUint32(mvhd[16:20], 1000)
	covr := atom("covr", atom("data", []byte{0, 0, 0, mime, 0, 0, 0, 0}, art))
	ilst := atom("ilst", append(entries, covr)...)
	return atom("moov", atom("mvhd", mvhd), atom("udta", atom("meta", make([]byte, 4), ilst)))
}

func TestPictureFLAC(t *testing.T) {
	art := []byte("jpeg-cover-bytes")
	path := write(t, "a.flac", flacWithPicture(pictureBlock("image/jpeg", art), "TITLE=One", "LYRICS=[00:01.00]Hello"))
	data, mime, err := Picture(path)
	if err != nil || mime != "image/jpeg" || !bytes.Equal(data, art) {
		t.Fatalf("picture = %q %q %v", data, mime, err)
	}
	// A picture block must not stop the tag walk that follows it.
	tags, err := Read(path)
	if err != nil || tags.Title != "One" || tags.Lyrics != "[00:01.00]Hello" {
		t.Fatalf("tags = %#v %v", tags, err)
	}
}

func TestPictureOpus(t *testing.T) {
	art := []byte("png-cover-bytes")
	encoded := base64.StdEncoding.EncodeToString(pictureBlock("image/png", art))
	path := write(t, "a.opus", opusFile(48000, "TITLE=One", pictureComment+"="+encoded))
	data, mime, err := Picture(path)
	if err != nil || mime != "image/png" || !bytes.Equal(data, art) {
		t.Fatalf("picture = %q %q %v", data, mime, err)
	}
}

func TestPictureMP4(t *testing.T) {
	art := []byte("jpeg-cover-bytes")
	path := write(t, "a.m4a", mp4WithCover(13, art, tagAtom("\xa9nam", "One"), tagAtom("\xa9lyr", "Plain words")))
	data, mime, err := Picture(path)
	if err != nil || mime != "image/jpeg" || !bytes.Equal(data, art) {
		t.Fatalf("picture = %q %q %v", data, mime, err)
	}
	tags, err := Read(path)
	if err != nil || tags.Lyrics != "Plain words" {
		t.Fatalf("tags = %#v %v", tags, err)
	}
}

// Art from an untrusted file must be rejected unless it is a type the server
// is willing to serve back, and a malformed block must not panic.
func TestPictureRejectsUnusableArt(t *testing.T) {
	cases := map[string][]byte{
		"unknown mime": flacWithPicture(pictureBlock("application/octet-stream", []byte("x"))),
		"empty image":  flacWithPicture(pictureBlock("image/jpeg", nil)),
		"no picture":   flacFile(44100, 44100, "TITLE=One"),
	}
	for name, body := range cases {
		if _, _, err := Picture(write(t, "a.flac", body)); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
	full := flacWithPicture(pictureBlock("image/jpeg", []byte("cover")), "TITLE=One")
	for n := range full {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("panic on %d truncated bytes: %v", n, p)
				}
			}()
			_, _, _ = Picture(write(t, "a.flac", full[:n]))
		}()
	}
}

func TestParseLyrics(t *testing.T) {
	lines := ParseLyrics("[ar:Someone]\r\n[00:12.50]First\n\n[01:00]Second\nPlain\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %#v", lines)
	}
	// 12.5s and 60s, in hundred-nanosecond ticks.
	if !lines[0].Timed || lines[0].Start != 125000000 || lines[0].Text != "First" {
		t.Fatalf("first = %#v", lines[0])
	}
	if lines[1].Start != 600000000 || lines[1].Text != "Second" {
		t.Fatalf("second = %#v", lines[1])
	}
	if lines[2].Timed || lines[2].Text != "Plain" {
		t.Fatalf("third = %#v", lines[2])
	}
	if got := ParseLyrics("just words"); len(got) != 1 || got[0].Timed {
		t.Fatalf("unsynced = %#v", got)
	}
}
