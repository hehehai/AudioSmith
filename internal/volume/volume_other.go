//go:build !(darwin || linux)

package volume

func existing(dir string) (string, bool) { return dir, dir != "" }
func bavail(string) int64                { return -1 }
func size(string) int64                  { return 0 }
func device(string) (uint64, bool)       { return 0, false }
func importantFree(string) int64         { return -1 }
