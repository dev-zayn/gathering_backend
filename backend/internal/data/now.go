package data

import "time"

// Now is the current time at the precision timestamptz keeps. Linux nanoseconds
// do not survive the round trip; macOS hides it, and CI runs on Linux.
func Now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
