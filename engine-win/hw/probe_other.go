//go:build !windows

package hw

import (
	"fmt"
	"runtime"
)

func probeHost() Info {
	return Classify(
		fmt.Sprintf("%s (%s)", runtime.GOOS, runtime.GOARCH),
		runtime.GOARCH,
		runtime.NumCPU(),
		8<<30, // conservative placeholder for non-Windows dev/test hosts
		4<<30,
		nil,
		Capabilities{CPU: true},
	)
}
