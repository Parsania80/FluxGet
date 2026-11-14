//go:build !linux

package engine

import "time"

type unsupportedHardwareReader struct{}

func newHardwareReader() hardwareReader { return unsupportedHardwareReader{} }
func (unsupportedHardwareReader) sample(now time.Time) hardwareSnapshot {
	return hardwareSnapshot{At: now, CPUState: "Unknown: Linux observations unavailable", MemoryState: "Unknown: Linux observations unavailable"}
}
