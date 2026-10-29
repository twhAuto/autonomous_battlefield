//go:build !race

// Tells catalog tests they run without the race detector.

package catalog

const raceEnabled = false
