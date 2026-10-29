//go:build !race

// Tells engine tests they run without the race detector.

package game

const raceEnabled = false
