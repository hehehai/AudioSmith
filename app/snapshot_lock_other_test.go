//go:build !unix && !windows

package main

import (
	"os"
	"testing"
)

func lockSnapFile(*testing.T, *os.File, string) {}
