package audio

import (
	"encoding/binary"
	"io"
)

// FLAC metadata block types, from the format specification.
const (
	blockStreamInfo = 0
	blockComment    = 4
)

// maxMetadataBlock bounds one metadata block of an untrusted file.
const maxMetadataBlock = 1 << 20

// readFLAC walks the metadata blocks that follow the "fLaC" marker. STREAMINFO
// carries an exact sample count, so FLAC durations need no estimation.
func readFLAC(r io.ReadSeeker) (Tags, error) {
	var t Tags
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil || string(magic[:]) != "fLaC" {
		return t, ErrUnsupported
	}
	for {
		var head [4]byte
		if _, err := io.ReadFull(r, head[:]); err != nil {
			return t, nil
		}
		last := head[0]&0x80 != 0
		size := int64(head[1])<<16 | int64(head[2])<<8 | int64(head[3])
		switch kind := head[0] & 0x7F; {
		case kind == blockStreamInfo && size <= maxMetadataBlock:
			b := make([]byte, size)
			if _, err := io.ReadFull(r, b); err != nil {
				return t, nil
			}
			readStreamInfo(b, &t)
		case kind == blockComment && size <= maxMetadataBlock:
			b := make([]byte, size)
			if _, err := io.ReadFull(r, b); err != nil {
				return t, nil
			}
			parseVorbisComments(b, &t)
		default:
			if _, err := r.Seek(size, io.SeekCurrent); err != nil {
				return t, nil
			}
		}
		if last {
			return t, nil
		}
	}
}

// readStreamInfo pulls the sample rate and total sample count out of
// STREAMINFO. They share eight bytes as a 20-bit rate, 3-bit channel count,
// 5-bit sample depth and 36-bit sample total.
func readStreamInfo(b []byte, t *Tags) {
	if len(b) < 18 {
		return
	}
	v := binary.BigEndian.Uint64(b[10:18])
	t.DurationTicks = durationTicks(v&0xF_FFFF_FFFF, v>>44)
}
