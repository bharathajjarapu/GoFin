package audio

import (
	"bytes"
	"encoding/binary"
	"io"
)

const (
	// oggPageHeader is the fixed part of a page header, before its segment table.
	oggPageHeader = 27
	// oggHeadRead covers the opening pages, which is where the identification
	// and comment packets live.
	oggHeadRead = 256 << 10
	// oggTailRead bounds the backwards search for the final page.
	oggTailRead = 64 << 10
	// oggMaxPayload bounds the reassembled header packets.
	oggMaxPayload = 1 << 20
	// opusRate is fixed: Opus granule positions always count 48 kHz samples,
	// whatever the source rate was.
	opusRate = 48000
)

var vorbisComment = []byte{3, 'v', 'o', 'r', 'b', 'i', 's'}

// readOgg parses an Ogg stream carrying either Vorbis or Opus. Tags come from
// the header packets at the start; the duration comes from the granule
// position on the last page, which counts the samples decoded so far.
func readOgg(r io.ReadSeeker, size int64) (Tags, error) {
	var t Tags
	head := make([]byte, min(int64(oggHeadRead), size))
	n, _ := io.ReadFull(r, head)
	if n == 0 {
		return t, ErrUnsupported
	}
	data := oggPayload(head[:n])
	var rate uint64
	var preSkip uint64
	switch {
	case bytes.HasPrefix(data, []byte("OpusHead")):
		if len(data) < 12 {
			return t, nil
		}
		rate, preSkip = opusRate, uint64(binary.LittleEndian.Uint16(data[10:12]))
		if i := bytes.Index(data, []byte("OpusTags")); i >= 0 {
			parseVorbisComments(data[i+len("OpusTags"):], &t)
		}
	case len(data) > 16 && data[0] == 1 && string(data[1:7]) == "vorbis":
		rate = uint64(binary.LittleEndian.Uint32(data[12:16]))
		if i := bytes.Index(data, vorbisComment); i >= 0 {
			parseVorbisComments(data[i+len(vorbisComment):], &t)
		}
	default:
		return t, ErrUnsupported
	}
	if granule, ok := oggLastGranule(r, size); ok && granule > preSkip {
		t.DurationTicks = durationTicks(granule-preSkip, rate)
	}
	return t, nil
}

// oggPayload concatenates the packet bytes of the pages in b. Header packets
// may span pages, and joining them lets the caller search one flat buffer.
func oggPayload(b []byte) []byte {
	out := make([]byte, 0, min(len(b), oggMaxPayload))
	for off := 0; off+oggPageHeader <= len(b) && len(out) < oggMaxPayload; {
		if string(b[off:off+4]) != "OggS" {
			break
		}
		table := off + oggPageHeader
		end := table + int(b[off+26])
		if end > len(b) {
			break
		}
		size := 0
		for _, segment := range b[table:end] {
			size += int(segment)
		}
		if end+size > len(b) {
			break
		}
		out = append(out, b[end:end+size]...)
		off = end + size
	}
	return out
}

// oggLastGranule reads the granule position of the final page in the stream.
func oggLastGranule(r io.ReadSeeker, size int64) (uint64, bool) {
	n := min(int64(oggTailRead), size)
	if n < oggPageHeader {
		return 0, false
	}
	if _, err := r.Seek(size-n, io.SeekStart); err != nil {
		return 0, false
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, false
	}
	i := bytes.LastIndex(buf, []byte("OggS"))
	if i < 0 || i+14 > len(buf) {
		return 0, false
	}
	return binary.LittleEndian.Uint64(buf[i+6 : i+14]), true
}
