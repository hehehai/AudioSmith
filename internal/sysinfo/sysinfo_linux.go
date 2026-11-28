//go:build linux

package sysinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

func readOS(s *Info) {
	s.OS = "Linux"
	if f, err := os.Open("/etc/os-release"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "PRETTY_NAME="); ok {
				s.OS = strings.Trim(v, `"`)
			}
		}
		f.Close()
	}
	if f, err := os.Open("/proc/cpuinfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if k, v, ok := strings.Cut(sc.Text(), ":"); ok && strings.TrimSpace(k) == "model name" {
				s.Chip = strings.TrimSpace(v)
				break
			}
		}
		f.Close()
	}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "MemTotal:"); ok {
				s.Memory = parseMeminfoKB(v) << 10
			} else if v, ok := strings.CutPrefix(line, "MemAvailable:"); ok {
				s.AvailMemory = parseMeminfoKB(v) << 10
			}
		}
		f.Close()
	}
}

func parseMeminfoKB(v string) int64 {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "kB"))
	kb, _ := strconv.ParseInt(v, 10, 64)
	return kb
}
