package neat

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func putSegment(index uint16, state uint32, start, end uint64) []byte {
	var rec [24]byte
	binary.LittleEndian.PutUint16(rec[0:], index)
	binary.LittleEndian.PutUint16(rec[2:], index)
	binary.LittleEndian.PutUint32(rec[4:], state)
	binary.LittleEndian.PutUint64(rec[8:], start)
	binary.LittleEndian.PutUint64(rec[16:], end)
	return rec[:]
}

func TestParseAndCover(t *testing.T) {
	b := append(putSegment(0, 1, 0, 15), putSegment(1, 0xffffffff, 16, 31)...)
	segs, err := ParseSegments(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCoverage(segs, 32); err != nil {
		t.Fatal(err)
	}
	if segs[1].State != 0xffffffff || segs[1].End != 31 {
		t.Fatalf("%+v", segs[1])
	}
	if err := ValidateCoverage(segs, 31); err == nil {
		t.Fatal("expected size mismatch")
	}
	gapped := append(putSegment(0, 1, 0, 10), putSegment(1, 1, 12, 20)...)
	bad, err := ParseSegments(gapped)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCoverage(bad, 21); err == nil {
		t.Fatal("expected a gap")
	}
}

func TestPartsFromFiles(t *testing.T) {
	dir := t.TempDir()
	segs := []Segment{{Index: 0, Start: 0, End: 15}, {Index: 1, Start: 16, End: 31}}
	if err := os.WriteFile(SegmentPath(dir, 0), []byte("abcd"), 0o644); err != nil {
		t.Fatal(err)
	}
	parts, err := PartsFromSegmentFiles(dir, segs)
	if err != nil {
		t.Fatal(err)
	}
	if parts[0].Have != 4 || parts[1].Have != 0 {
		t.Fatalf("%+v", parts)
	}
	if err := os.WriteFile(SegmentPath(dir, 1), bytes.Repeat([]byte{'x'}, 20), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PartsFromSegmentFiles(dir, segs); err == nil {
		t.Fatal("expected oversized segment file to fail")
	}
}

func TestSplit(t *testing.T) {
	parts, err := Split(10, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, p := range parts {
		n += p.Span()
	}
	if n != 10 || parts[0].Start != 0 || parts[len(parts)-1].End != 9 {
		t.Fatalf("%+v", parts)
	}
	one, err := Split(10, 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 {
		t.Fatalf("min chunk should collapse to one part: %+v", one)
	}
}

func TestCopyPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seg.x0"), []byte("abcdefghij"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := make([]byte, 16)
	w := writerAt{b: dst}
	seg := Segment{Index: 0, Start: 4, End: 15}
	if err := CopySegmentPrefix(w, dir, seg, 4); err != nil {
		t.Fatal(err)
	}
	if string(dst[4:8]) != "abcd" {
		t.Fatalf("%q", dst)
	}
}

type writerAt struct{ b []byte }

func (w writerAt) WriteAt(p []byte, off int64) (int, error) {
	return copy(w.b[off:], p), nil
}
