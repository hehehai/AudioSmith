package services

import "github.com/hehehai/AudioSmith/internal/volume"

// diskSize is the size of the volume of dir (or of its nearest existing
// parent), 0 when unknown.
func diskSize(dir string) int64 { return volume.Size(dir) }

// diskFree is the space free to the user on the volume of dir (or of its
// nearest existing parent; on macOS with APFS's purgeable space: code
// review 1103 M-3), -1 when unknown.
func diskFree(dir string) int64 { return volume.Free(dir) }
