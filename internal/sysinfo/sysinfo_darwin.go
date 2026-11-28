//go:build darwin

package sysinfo

import (
	"os/exec"
	"strconv"
	"strings"
)

func readOS(s *Info) {
	run := func(name string, args ...string) string {
		out, err := exec.Command(name, args...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	if v := run("/usr/bin/sw_vers", "-productVersion"); v != "" {
		s.OS = "macOS " + v
	}
	s.Chip = run("/usr/sbin/sysctl", "-n", "machdep.cpu.brand_string")
	s.Memory, _ = strconv.ParseInt(run("/usr/sbin/sysctl", "-n", "hw.memsize"), 10, 64)
	if s.Arch == "arm64" && (strings.HasPrefix(s.Chip, "Apple") || s.Chip == "") {
		name := s.Chip
		if name == "" {
			name = "Apple Silicon"
		}
		s.GPUs = []GPU{{
			Name:         name,
			Vendor:       VendorApple,
			VendorID:     0x106B,
			SharedMemory: s.Memory,
			Integrated:   true,
		}}
		s.GPUClass = GPUClassApple
	}
}
