//go:build race

package acp

// The race-instrumented child takes about a second to start, so the stdio
// lifecycle tests run fewer iterations under the detector.

const raceDetector = true
