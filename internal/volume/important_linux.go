package volume

// importantFree is macOS's alone: Linux has no purgeable space.
func importantFree(string) int64 { return -1 }
