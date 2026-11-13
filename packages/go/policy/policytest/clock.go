// Package policytest provides a manual clock for deterministic limit tests.
package policytest

import (
	"sort"
	"sync"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/policy"
)

// Clock only moves when Advance is called. Due callbacks run synchronously in the
// advancing goroutine, in deadline order, outside the clock's lock, so a callback may
// schedule or stop other timers.
type Clock struct {
	mu     sync.Mutex
	now    time.Time
	nextID uint64
	timers map[uint64]*timer
}

type timer struct {
	clock *Clock
	id    uint64
	at    time.Time
	f     func()
}

var _ policy.Clock = (*Clock)(nil)

func New(start time.Time) *Clock {
	return &Clock{now: start, timers: map[uint64]*timer{}}
}

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) AfterFunc(d time.Duration, f func()) policy.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	t := &timer{clock: c, id: c.nextID, at: c.now.Add(d), f: f}
	c.timers[t.id] = t
	return t
}

func (t *timer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	_, pending := t.clock.timers[t.id]
	delete(t.clock.timers, t.id)
	return pending
}

// Pending reports how many timers are scheduled and not yet fired or stopped.
func (c *Clock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

// Advance moves time forward by d, firing every timer that becomes due, including
// timers scheduled by callbacks within the window.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var due []*timer
		for _, t := range c.timers {
			if !t.at.After(target) {
				due = append(due, t)
			}
		}
		if len(due) == 0 {
			if target.After(c.now) {
				c.now = target
			}
			c.mu.Unlock()
			return
		}
		sort.Slice(due, func(i, j int) bool {
			if due[i].at.Equal(due[j].at) {
				return due[i].id < due[j].id
			}
			return due[i].at.Before(due[j].at)
		})
		next := due[0]
		delete(c.timers, next.id)
		if next.at.After(c.now) {
			c.now = next.at
		}
		c.mu.Unlock()
		next.f()
	}
}
