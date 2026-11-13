package media

import (
	"errors"
	"sync"
	"testing"
)

type closeRecord struct {
	mu    sync.Mutex
	calls []string
	errs  []error
}

func (c *closeRecord) on(id string, err error) {
	c.mu.Lock()
	c.calls, c.errs = append(c.calls, id), append(c.errs, err)
	c.mu.Unlock()
}

// OnClose fires exactly once per verified stream, after recognition has closed:
// nil for a clean stop, an error when the socket drops (#36).
func TestOnCloseDistinguishesStopFromDisconnect(t *testing.T) {
	for _, tc := range []struct {
		name  string
		end   func(*pipe)
		clean bool
	}{
		{"stop", func(p *pipe) {
			data := []byte(`{"event":"stop","sequenceNumber":"2","streamSid":"` + stream + `"}`)
			p.in <- data
		}, true},
		{"socket drop", func(p *pipe) { close(p.in) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, tr, rec := newPipe(), &fakeTranscriber{}, newRecorder()
			closed := &closeRecord{}
			cfg := rec.config(tr, nil)
			cfg.OnClose = func(id string, err error) {
				tr.mu.Lock()
				sessionClosed := tr.closed
				tr.mu.Unlock()
				if !sessionClosed {
					t.Error("OnClose ran before the recognition session closed")
				}
				closed.on(id, err)
			}
			done, cancel := serve(cfg, p)
			defer cancel()
			p.send(t, startMsg(1, validParams(), nil))
			<-rec.started
			tc.end(p)
			err := wait(t, done)
			closed.mu.Lock()
			defer closed.mu.Unlock()
			if len(closed.calls) != 1 || closed.calls[0] != callID {
				t.Fatalf("OnClose calls = %v", closed.calls)
			}
			if (closed.errs[0] == nil) != tc.clean || !errors.Is(closed.errs[0], err) && err != nil {
				t.Fatalf("OnClose err = %v, Serve err = %v", closed.errs[0], err)
			}
		})
	}
}

func TestOnCloseNeverFiresForUnverifiedStreams(t *testing.T) {
	p, tr, rec := newPipe(), &fakeTranscriber{}, newRecorder()
	cfg := rec.config(tr, nil)
	fired := false
	cfg.OnClose = func(string, error) { fired = true }
	done, cancel := serve(cfg, p)
	defer cancel()
	p.send(t, startMsg(1, map[string]string{"call_id": callID, "token": "forged"}, nil))
	if err := wait(t, done); !errors.Is(err, ErrUnauthorized) || fired {
		t.Fatalf("err=%v fired=%v", err, fired)
	}
}
