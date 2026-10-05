package neat

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Segment is one range from segments.bin. End is inclusive.
type Segment struct {
	Index uint16
	State uint32
	Start int64
	End   int64
}

// Part is a byte range to fetch. End is inclusive. End < 0 means the
// server did not say how large the file is. Have is how many bytes,
// starting at Start, are already on disk.
type Part struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
	Have  int64 `json:"have"`
}

// Span is the number of bytes in the part. A negative end has no known span.
func (p Part) Span() int64 {
	if p.End < 0 {
		return -1
	}
	return p.End - p.Start + 1
}

// ParseSegments decodes segments.bin.
func ParseSegments(b []byte) ([]Segment, error) {
	if len(b) == 0 {
		return nil, errors.New("segments.bin is empty")
	}
	if len(b)%24 != 0 {
		return nil, fmt.Errorf("segments.bin length %d is not a multiple of 24", len(b))
	}
	out := make([]Segment, 0, len(b)/24)
	for off := 0; off < len(b); off += 24 {
		rec := b[off : off+24]
		index := binary.LittleEndian.Uint16(rec[0:2])
		state := binary.LittleEndian.Uint32(rec[4:8])
		start := int64(binary.LittleEndian.Uint64(rec[8:16]))
		end := int64(binary.LittleEndian.Uint64(rec[16:24]))
		if start < 0 || end < start {
			return nil, fmt.Errorf("segment %d has range %d-%d", index, start, end)
		}
		out = append(out, Segment{Index: index, State: state, Start: start, End: end})
	}
	return out, nil
}

// LoadSegments reads <dir>/segments.bin.
func LoadSegments(dir string) ([]Segment, error) {
	b, err := os.ReadFile(filepath.Join(dir, "segments.bin"))
	if err != nil {
		return nil, fmt.Errorf("read segments.bin: %w", err)
	}
	return ParseSegments(b)
}

// ValidateCoverage checks that segments tile [0, fileSize) without gaps.
func ValidateCoverage(segs []Segment, fileSize int64) error {
	if fileSize <= 0 {
		return errors.New("file size is unknown, cannot adopt segments")
	}
	if len(segs) == 0 {
		return errors.New("no segments")
	}
	sorted := append([]Segment(nil), segs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	if sorted[0].Start != 0 {
		return fmt.Errorf("first segment starts at %d", sorted[0].Start)
	}
	seen := make(map[uint16]struct{}, len(sorted))
	prev := int64(-1)
	for _, s := range sorted {
		if _, ok := seen[s.Index]; ok {
			return fmt.Errorf("duplicate segment index %d", s.Index)
		}
		seen[s.Index] = struct{}{}
		if prev >= 0 && s.Start != prev+1 {
			return fmt.Errorf("gap or overlap at byte %d (segment %d starts at %d)", prev+1, s.Index, s.Start)
		}
		prev = s.End
	}
	if prev+1 != fileSize {
		return fmt.Errorf("segments cover %d bytes, database says %d", prev+1, fileSize)
	}
	return nil
}

// SegmentPath is <dir>/seg.xN.
func SegmentPath(dir string, index uint16) string {
	return filepath.Join(dir, fmt.Sprintf("seg.x%d", index))
}

// PartsFromSegmentFiles builds parts from segments.bin and the size of
// each seg.xN file. A missing seg.xN means nothing has been saved yet.
// A seg.xN longer than its range is an error: the record layout would
// be wrong and copying it would corrupt the file.
func PartsFromSegmentFiles(dir string, segs []Segment) ([]Part, error) {
	parts := make([]Part, 0, len(segs))
	for _, s := range segs {
		span := s.End - s.Start + 1
		p := Part{Start: s.Start, End: s.End}
		st, err := os.Stat(SegmentPath(dir, s.Index))
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		} else {
			if st.Size() > span {
				return nil, fmt.Errorf("seg.x%d is %d bytes, range holds %d", s.Index, st.Size(), span)
			}
			p.Have = st.Size()
		}
		parts = append(parts, p)
	}
	return parts, nil
}

// CopySegmentPrefix copies the first part.Have bytes of seg.xN into w
// at part.Start. have must already match the file, or be shorter.
func CopySegmentPrefix(dst io.WriterAt, dir string, seg Segment, have int64) error {
	if have <= 0 {
		return nil
	}
	span := seg.End - seg.Start + 1
	if have > span {
		return fmt.Errorf("seg.x%d prefix %d exceeds range %d", seg.Index, have, span)
	}
	f, err := os.Open(SegmentPath(dir, seg.Index))
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 256*1024)
	var off int64
	for off < have {
		n := int64(len(buf))
		if have-off < n {
			n = have - off
		}
		nr, rerr := f.Read(buf[:n])
		if nr > 0 {
			nw, werr := dst.WriteAt(buf[:nr], seg.Start+off)
			off += int64(nw)
			if werr != nil {
				return werr
			}
			if nw != nr {
				return io.ErrShortWrite
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if off != have {
		return fmt.Errorf("seg.x%d: copied %d bytes, file has %d", seg.Index, off, have)
	}
	return nil
}

// Split divides [0, size) into n ranges. minChunk keeps tiny files on
// one connection. n is reduced until each part is at least minChunk,
// except when the file itself is smaller than that.
func Split(size int64, n int, minChunk int64) ([]Part, error) {
	if size <= 0 {
		return nil, errors.New("cannot split an unknown size")
	}
	if n < 1 {
		n = 1
	}
	if minChunk < 1 {
		minChunk = 1
	}
	if int64(n) > size {
		n = int(size)
	}
	maxN := size / minChunk
	if maxN < 1 {
		maxN = 1
	}
	if int64(n) > maxN {
		n = int(maxN)
	}
	base := size / int64(n)
	rem := size % int64(n)
	parts := make([]Part, 0, n)
	var off int64
	for i := 0; i < n; i++ {
		sz := base
		if int64(i) < rem {
			sz++
		}
		parts = append(parts, Part{Start: off, End: off + sz - 1})
		off += sz
	}
	if off != size {
		return nil, fmt.Errorf("internal split error: covered %d of %d", off, size)
	}
	return parts, nil
}
