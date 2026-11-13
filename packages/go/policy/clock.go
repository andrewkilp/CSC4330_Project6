package policy

import "time"

// Timer is a scheduled callback. Stop reports whether it prevented the callback.
type Timer interface {
	Stop() bool
}

// Clock is the time source for limits. Production uses SystemClock; tests use
// policytest.Clock so duration limits can be proven without sleeping.
type Clock interface {
	Now() time.Time
	// AfterFunc runs f in its own goroutine (or, for a fake clock, the advancing
	// goroutine) once d has elapsed, unless the returned Timer is stopped first.
	AfterFunc(d time.Duration, f func()) Timer
}

// SystemClock is the wall clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

func (SystemClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }
