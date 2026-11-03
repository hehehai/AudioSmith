package hub

import "github.com/hehehai/AudioSmith/internal/volume"

// freeBytes is the space available to the user on the volume of dir (or
// of its nearest existing parent; on macOS with APFS's purgeable space:
// code review 1103 M-3), -1 when unknown.
func freeBytes(dir string) int64 { return volume.Free(dir) }
