package telephony

import (
	"errors"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
)

func TestRealStartConfigurationRejectsNonCanonicalURLs(t *testing.T) {
	real, err := New(config())
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"https://demo.example?", "https://demo.example?x=1", "https://demo.example/path", "https://user:secret@demo.example", "https://demo.example#fragment", "http://demo.example"} {
		_, err = NewStartService(nil, real, mockProvider(t), StartConfig{EnableReal: true, AllowedNumbers: []string{"+12255550100"}, PublicBaseURL: base, AccountSID: testAccount})
		if !errors.Is(err, providers.ErrInvalid) {
			t.Fatalf("accepted noncanonical public origin: %q", base)
		}
	}
}
