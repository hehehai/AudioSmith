// Package volume measures the disk (volume) a folder is on: its size and
// the space free to the user (code review 1103 M-3), and whether two
// folders share one.
package volume

// Free is the space free to the user on the volume of dir (or of its
// nearest existing parent), -1 when unknown. On macOS it is what Finder
// shows: statfs's f_bavail leaves out APFS's purgeable space (local
// snapshots, caches the system clears when room is needed), so Free is
// the larger of it and the volume's "available capacity for important
// usage", which counts it (code review 1103 M-3).
func Free(dir string) int64 {
	p, ok := existing(dir)
	if !ok {
		return -1
	}
	free := bavail(p)
	if imp := importantFree(p); imp > free {
		free = imp
	}
	return free
}

// Size is the size of the volume of dir (or of its nearest existing
// parent), 0 when unknown.
func Size(dir string) int64 {
	p, ok := existing(dir)
	if !ok {
		return 0
	}
	return size(p)
}

// Same reports whether a and b (or their nearest existing parents) are on
// one volume; false when unknown.
func Same(a, b string) bool {
	pa, ok1 := existing(a)
	pb, ok2 := existing(b)
	if !ok1 || !ok2 {
		return false
	}
	da, ok1 := device(pa)
	db, ok2 := device(pb)
	return ok1 && ok2 && da == db
}
