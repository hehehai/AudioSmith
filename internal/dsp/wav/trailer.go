package wav

import "io"

// A header written as it streamed (data size 0 or 0xFFFFFFFF), or one
// claiming more than the file holds, leaves the samples' end unknown;
// reading to the end of the file would take any chunk after the data
// (LIST, id3 , bext…) for samples (code review 1103 F8). samplesEnd finds
// such trailing chunks instead.

// trailerWindow is how far from the end of the file a trailing chunk is
// looked for.
const trailerWindow = 4 << 20

// trailerIDs are chunks found after the samples (the first one of the
// trailer must be one of them; those after it need only a printable id).
var trailerIDs = map[string]bool{
	"LIST": true, "id3 ": true, "ID3 ": true, "bext": true, "iXML": true, "axml": true, "cue ": true,
	"smpl": true, "inst": true, "acid": true, "junk": true, "JUNK": true, "PAD ": true, "FLLR": true,
	"fact": true, "_PMX": true, "cart": true, "umid": true, "PEAK": true, "DISP": true, "plst": true,
	"labl": true, "note": true, "ltxt": true, "minf": true, "elm1": true, "regn": true, "levl": true,
}

// samplesEnd returns the byte offset where the samples that start at data
// end in a file of size bytes whose frames are fb bytes: the start of a
// run of chunks after them that ends exactly at the end of the file (at a
// whole frame from data, or one pad byte after), else size.
func samplesEnd(r io.ReaderAt, data, size, fb int64) int64 {
	start := max(data, size-trailerWindow)
	if size-start < 8 || fb <= 0 {
		return size
	}
	tail := make([]byte, size-start)
	if n, err := r.ReadAt(tail, start); int64(n) != size-start && err != nil {
		return size
	}
	first := data + (start-data)/fb*fb
	for k := first; k+8 <= size; k += fb {
		for _, p := range []int64{k, k + 1} {
			if p < start || p+8 > size || (p == k+1 && (k-data)%2 == 0) {
				continue // the pad byte only follows an odd-sized data chunk
			}
			if trailerIDs[string(tail[p-start:p-start+4])] && chainEnds(tail, p-start) {
				return p
			}
		}
	}
	return size
}

// chainEnds reports whether the chunks from b[at:] follow each other to
// exactly the end of b.
func chainEnds(b []byte, at int64) bool {
	n := int64(len(b))
	for range 256 {
		if at+8 > n {
			return false
		}
		for _, c := range b[at : at+4] {
			if c < 0x20 || c > 0x7e {
				return false
			}
		}
		sz := int64(b[at+4]) | int64(b[at+5])<<8 | int64(b[at+6])<<16 | int64(b[at+7])<<24
		next := at + 8 + sz + sz&1
		if next == n || next == n+1 && sz&1 == 1 { // the last pad byte may be missing
			return true
		}
		if next > n {
			return false
		}
		at = next
	}
	return false
}
