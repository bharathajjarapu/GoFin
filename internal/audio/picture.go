package audio

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"io"
	"os"
	"strings"
)

const (
	// blockPicture is the FLAC metadata block holding cover art.
	blockPicture = 6
	// maxPictureBytes bounds a cover read from a file we do not control.
	maxPictureBytes = 8 << 20
	// oggPictureRead covers enough of an Ogg stream to reach an embedded cover,
	// which dwarfs the tags and is inflated by a third again by base64.
	oggPictureRead = 4 << 20
	// pictureComment is the Vorbis comment Ogg files carry cover art in.
	pictureComment = "METADATA_BLOCK_PICTURE"
	// pictureHeader covers the four image dimensions between a picture's
	// description and its bytes: width, height, colour depth and colour count.
	pictureHeader = 16
)

// pictureMimes are the image types the server will store and serve back.
// Anything else is ignored rather than written to disk under a type that
// would then be echoed to a browser.
var pictureMimes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// PictureExt returns the file extension for an image type Picture returned.
func PictureExt(mime string) string { return pictureMimes[mime] }

// Picture returns the cover art embedded in an audio file. It is deliberately
// separate from Read: a scan reads tags for every track but needs art only once
// per album, and a cover is thousands of times larger than the tags beside it.
func Picture(path string) ([]byte, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	switch Ext(path) {
	case "flac":
		return flacPicture(f)
	case "ogg", "oga", "opus":
		return oggPicture(f, info.Size())
	case "m4a", "m4b":
		return mp4Picture(f, info.Size())
	}
	return nil, "", ErrUnsupported
}

// flacPicture walks the metadata blocks looking for a PICTURE block, the same
// layout Ogg carries base64 encoded.
func flacPicture(r io.ReadSeeker) ([]byte, string, error) {
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil || string(magic[:]) != "fLaC" {
		return nil, "", ErrUnsupported
	}
	for {
		var head [4]byte
		if _, err := io.ReadFull(r, head[:]); err != nil {
			return nil, "", ErrUnsupported
		}
		last := head[0]&0x80 != 0
		size := int64(head[1])<<16 | int64(head[2])<<8 | int64(head[3])
		if head[0]&0x7F == blockPicture && size <= maxPictureBytes {
			b := make([]byte, size)
			if _, err := io.ReadFull(r, b); err != nil {
				return nil, "", ErrUnsupported
			}
			return parsePictureBlock(b)
		}
		if last {
			return nil, "", ErrUnsupported
		}
		if _, err := r.Seek(size, io.SeekCurrent); err != nil {
			return nil, "", ErrUnsupported
		}
	}
}

// oggPicture decodes the base64 METADATA_BLOCK_PICTURE comment.
func oggPicture(r io.ReadSeeker, size int64) ([]byte, string, error) {
	head := make([]byte, min(int64(oggPictureRead), size))
	n, _ := io.ReadFull(r, head)
	if n == 0 {
		return nil, "", ErrUnsupported
	}
	comments, ok := oggComments(oggPayload(head[:n], oggPictureRead))
	if !ok {
		return nil, "", ErrUnsupported
	}
	encoded, ok := findVorbisComment(comments, pictureComment)
	if !ok {
		return nil, "", ErrUnsupported
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", ErrUnsupported
	}
	return parsePictureBlock(raw)
}

// oggComments locates the comment payload inside reassembled Ogg header
// packets, which Opus and Vorbis introduce with different markers.
func oggComments(data []byte) ([]byte, bool) {
	for _, marker := range [][]byte{[]byte("OpusTags"), vorbisComment} {
		if i := bytes.Index(data, marker); i >= 0 {
			return data[i+len(marker):], true
		}
	}
	return nil, false
}

// parsePictureBlock reads the FLAC PICTURE layout: a picture type, a
// length-prefixed mime string, a length-prefixed description, four image
// dimensions, then the image itself. Every field is checked against what is
// left of the buffer, because the block comes from an untrusted file.
func parsePictureBlock(b []byte) ([]byte, string, error) {
	take := func(n int) ([]byte, bool) {
		if n < 0 || len(b) < n {
			return nil, false
		}
		out := b[:n]
		b = b[n:]
		return out, true
	}
	field := func() ([]byte, bool) {
		head, ok := take(4)
		if !ok {
			return nil, false
		}
		size := binary.BigEndian.Uint32(head)
		if uint64(size) > uint64(len(b)) {
			return nil, false
		}
		return take(int(size))
	}
	if _, ok := take(4); !ok {
		return nil, "", ErrUnsupported
	}
	mime, ok := field()
	if !ok {
		return nil, "", ErrUnsupported
	}
	if _, ok := field(); !ok {
		return nil, "", ErrUnsupported
	}
	if _, ok := take(pictureHeader); !ok {
		return nil, "", ErrUnsupported
	}
	data, ok := field()
	if !ok {
		return nil, "", ErrUnsupported
	}
	return picture(data, strings.ToLower(strings.TrimSpace(string(mime))))
}

// mp4Picture follows the moov/udta/meta/ilst path to the covr atom.
func mp4Picture(r io.ReadSeeker, size int64) ([]byte, string, error) {
	start, end := int64(0), size
	for _, name := range []string{"moov", "udta", "meta", "ilst", "covr"} {
		body, next, ok := findAtom(r, start, end, name)
		if !ok {
			return nil, "", ErrUnsupported
		}
		if name == "meta" {
			// meta carries a version and flags word ahead of its children.
			body += 4
		}
		start, end = body, next
	}
	return mp4PictureData(r, start, end)
}

// findAtom scans one level of the atom tree for name, returning where its
// payload starts and where the atom ends.
func findAtom(r io.ReadSeeker, start, end int64, name string) (int64, int64, bool) {
	for off := start; off+atomHeader <= end; {
		size, got, body, ok := readAtomHeader(r, off, end)
		if !ok || size <= 0 {
			return 0, 0, false
		}
		if got == name {
			return body, off + size, true
		}
		off += size
	}
	return 0, 0, false
}

// mp4PictureData reads the data atom of covr, whose type flag names the image
// format: 13 is JPEG and 14 is PNG.
func mp4PictureData(r io.ReadSeeker, start, end int64) ([]byte, string, error) {
	for off := start; off+atomHeader <= end; {
		size, name, body, ok := readAtomHeader(r, off, end)
		if !ok || size <= 0 {
			return nil, "", ErrUnsupported
		}
		if name == "data" {
			flags, ok := readAt(r, body, body+4, 4)
			if !ok {
				return nil, "", ErrUnsupported
			}
			data, ok := readAt(r, off+dataAtomPrefix, off+size, maxPictureBytes)
			if !ok {
				return nil, "", ErrUnsupported
			}
			return picture(data, mp4PictureMimes[flags[3]])
		}
		off += size
	}
	return nil, "", ErrUnsupported
}

// mp4PictureMimes maps the iTunes data-atom type flag to an image type.
var mp4PictureMimes = map[byte]string{13: "image/jpeg", 14: "image/png"}

// picture accepts an image only if it is a type the server will serve back and
// small enough to be a cover rather than a payload.
func picture(data []byte, mime string) ([]byte, string, error) {
	if len(data) == 0 || len(data) > maxPictureBytes || pictureMimes[mime] == "" {
		return nil, "", ErrUnsupported
	}
	return data, mime, nil
}
