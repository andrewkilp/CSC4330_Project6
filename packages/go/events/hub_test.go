package events

import (
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

const call = "0b6c4f7e-3f1a-4c2b-9d3e-1a2b3c4d5e6f"

func TestFanoutToEverySubscriberOfTheCall(t *testing.T) {
	h := NewHub(4)
	a, b := h.Subscribe(call), h.Subscribe(call)
	other := h.Subscribe("1c7d5a8f-4a2b-4d3c-8e4f-2b3c4d5e6f70")
	h.Publish(repository.Event{CallID: call, Seq: 1}, repository.Event{CallID: call, Seq: 2})
	for _, s := range []*Subscription{a, b} {
		if e := <-s.C; e.Seq != 1 {
			t.Fatalf("first = %d", e.Seq)
		}
		if e := <-s.C; e.Seq != 2 {
			t.Fatalf("second = %d", e.Seq)
		}
	}
	select {
	case e := <-other.C:
		t.Fatalf("another call received %+v", e)
	default:
	}
	if h.Subscribers(call) != 2 {
		t.Fatal("subscriber count")
	}
	a.Close()
	a.Close()
	if _, open := <-a.C; open || h.Subscribers(call) != 1 {
		t.Fatal("close did not unregister")
	}
}

func TestSlowSubscriberIsDroppedNotBlocking(t *testing.T) {
	h := NewHub(2)
	slow, fast := h.Subscribe(call), h.Subscribe(call)
	for seq := int64(1); seq <= 3; seq++ {
		h.Publish(repository.Event{CallID: call, Seq: seq})
		<-fast.C // the fast reader keeps up
	}
	if !slow.Lagged() || fast.Lagged() {
		t.Fatalf("lagged: slow=%v fast=%v", slow.Lagged(), fast.Lagged())
	}
	n := 0
	for range slow.C {
		n++
	}
	if n != 2 || h.Subscribers(call) != 1 {
		t.Fatalf("slow buffered %d, subscribers %d", n, h.Subscribers(call))
	}
	slow.Close() // safe after the hub closed it
}
