//go:build !darwin && !linux

package services

// diskSize is unknown here.
func diskSize(string) int64 { return 0 }
