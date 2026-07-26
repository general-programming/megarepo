package device

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsRefusalClassifiesEachGuard(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"writes not allowed", &WritesNotAllowedError{What: "a writer"}, true},
		{"write attempt", &WriteAttemptError{What: "a command"}, true},
		{"unmanaged command", &UnmanagedCommandError{Command: "no ip routing"}, true},
		{"pre-send, bare", &PreSendError{What: "refusing an empty path"}, true},
		{"pre-send, wrapping a cause", &PreSendError{What: "resolving the API key", Err: errors.New("vault down")}, true},
		{"an ordinary error", errors.New("commit failed"), false},
		{"a wrapped ordinary error", fmt.Errorf("deploy: %w", errors.New("timeout")), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRefusal(tc.err); got != tc.want {
				t.Errorf("IsRefusal(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// PreSendError must still let errors.As/Is reach the wrapped cause, not just
// ErrPreSend: a caller may want to know it was, say, a vault.ErrNotFound.
func TestPreSendErrorUnwrapsToItsCause(t *testing.T) {
	cause := errors.New("vault: secret not found")
	err := &PreSendError{What: "resolving the API key", Err: cause}

	if !errors.Is(err, ErrPreSend) {
		t.Error("errors.Is(err, ErrPreSend) = false")
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is(err, cause) = false; the underlying cause must stay reachable")
	}
}
