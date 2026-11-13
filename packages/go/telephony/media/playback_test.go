package media

import (
	"errors"
	"testing"
)

func TestPlaybackCountsMarksInOrderAndCompletes(t *testing.T) {
	p, err := NewPlayback("3d2e9e65-7f30-4ca1-9e80-4b5c6d7e8f92")
	if err != nil {
		t.Fatal(err)
	}
	var marks []string
	for _, n := range []int{1600, 1600, 800} {
		m, err := p.Queue(n)
		if err != nil || !markName.MatchString(m) {
			t.Fatalf("mark %q: %v (must satisfy the bridge's mark pattern)", m, err)
		}
		marks = append(marks, m)
	}
	p.Finish()
	if _, err := p.Queue(10); !errors.Is(err, ErrStaleMark) {
		t.Fatal("queued after finish")
	}
	if _, err := p.Acknowledge(marks[1]); !errors.Is(err, ErrStaleMark) {
		t.Fatal("out-of-order mark accepted")
	}
	for i, m := range marks {
		done, err := p.Acknowledge(m)
		if err != nil || done != (i == 2) {
			t.Fatalf("ack %d: done=%v err=%v", i, done, err)
		}
	}
	if played, queued := p.Progress(); played != 4000 || queued != 4000 || p.Active() {
		t.Fatalf("progress %d/%d active=%v", played, queued, p.Active())
	}
	if _, err := p.Acknowledge(marks[2]); !errors.Is(err, ErrStaleMark) {
		t.Fatal("repeated mark accepted")
	}
}

func TestClearFreezesProgressAndStalesOutstandingMarks(t *testing.T) {
	p, _ := NewPlayback("r1")
	a, _ := p.Queue(1000)
	b, _ := p.Queue(1000)
	if _, err := p.Acknowledge(a); err != nil {
		t.Fatal(err)
	}
	played, queued := p.Clear()
	if played != 1000 || queued != 2000 || p.Active() {
		t.Fatalf("clear = %d/%d", played, queued)
	}
	if _, err := p.Acknowledge(b); !errors.Is(err, ErrStaleMark) {
		t.Fatal("late mark for cleared audio accepted")
	}
	if _, err := p.Queue(10); !errors.Is(err, ErrStaleMark) {
		t.Fatal("cleared playback accepted more audio")
	}
	if again, _ := p.Clear(); again != 1000 {
		t.Fatal("second clear changed progress")
	}
	for _, bad := range []string{"", "r1", "r1.x", "r2.1", "r1.0"} {
		if _, err := p.Acknowledge(bad); !errors.Is(err, ErrStaleMark) {
			t.Errorf("mark %q accepted", bad)
		}
	}
	if _, err := NewPlayback("bad id with spaces"); err == nil {
		t.Fatal("invalid id accepted")
	}
}
