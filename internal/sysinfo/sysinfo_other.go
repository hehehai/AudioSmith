//go:build !darwin && !linux && !windows

package sysinfo

import "runtime"

func readOS(s *Info) {
	s.OS = runtime.GOOS
}
