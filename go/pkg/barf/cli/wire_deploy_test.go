package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
)

// fakeDeviceWriter is device.Writer, standing in for a real transport so
// writerAdapter.Configure's error classification can be tested without one.
type fakeDeviceWriter struct {
	configureErr error
}

func (f fakeDeviceWriter) Configure(context.Context, []device.Op) error { return f.configureErr }
func (f fakeDeviceWriter) SaveConfig(context.Context) error             { return nil }

// Regression: writerAdapter.Configure wrapped EVERY error in
// unsavedConfigError ("device may have applied this config anyway ... NOT
// saved"), including provable pre-send refusals that device.IsRefusal
// exists to identify — a closed allowlist or a lazy secret/endpoint lookup
// failing means nothing reached the device at all.
func TestWriterAdapterConfigureClassifiesRefusals(t *testing.T) {
	cases := []struct {
		name           string
		err            error
		wantUnsaved    bool
		wantMsgContain string
	}{
		{
			name:           "a closed-allowlist refusal is not a maybe-applied warning",
			err:            &device.WriteAttemptError{What: "VyOS configure op[0] verb \"frob\""},
			wantUnsaved:    false,
			wantMsgContain: "refused",
		},
		{
			name:           "a pre-send failure (e.g. a lazy API key fetch) is not a maybe-applied warning",
			err:            &device.PreSendError{What: "resolving the VyOS API key", Err: errors.New("vault down")},
			wantUnsaved:    false,
			wantMsgContain: "refused",
		},
		{
			name:           "an in-flight failure IS a maybe-applied warning",
			err:            errors.New("connection reset by peer"),
			wantUnsaved:    true,
			wantMsgContain: "may have applied this config anyway",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := writerAdapter{w: fakeDeviceWriter{configureErr: tc.err}, hostname: "sea1-vpn-0"}
			err := a.Configure(context.Background(), nil)
			if err == nil {
				t.Fatal("want an error")
			}
			var unsaved unsavedConfigError
			isUnsaved := errors.As(err, &unsaved)
			if isUnsaved != tc.wantUnsaved {
				t.Errorf("errors.As(err, *unsavedConfigError) = %v, want %v (err: %v)", isUnsaved, tc.wantUnsaved, err)
			}
			if !strings.Contains(err.Error(), tc.wantMsgContain) {
				t.Errorf("err = %q, want it to contain %q", err.Error(), tc.wantMsgContain)
			}
			// Whichever shape it took, the underlying device error must
			// still be reachable: callers may want to inspect it further.
			if !errors.Is(err, tc.err) {
				t.Errorf("err does not wrap the original device error")
			}
		})
	}
}
