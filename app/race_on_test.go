//go:build race

package main

// raceEnabled: built with -race; wall-clock perf bounds are not checked.
const raceEnabled = true
