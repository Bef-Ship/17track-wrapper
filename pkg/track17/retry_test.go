package track17

import (
	"context"
	"errors"
	"fmt"
	"testing"

	sdk "github.com/SolaTyolo/17track-sdk"
)

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{fmt.Errorf("17track: unexpected http status 429: slow down"), true},
		{fmt.Errorf("17track: unexpected http status 503: unavailable"), true},
		{fmt.Errorf("17track: unexpected http status 400: bad"), false},
		{fmt.Errorf("17track: http do: dial tcp: i/o timeout"), true},
		{context.DeadlineExceeded, true},
		{context.Canceled, false},
		{&sdk.APIError{Code: -18019903, Message: "carrier unrecognized"}, false},
		{&sdk.APIError{Code: -1, Message: "Too many requests"}, true},
		{errors.Join(ErrRetryable, errors.New("x")), true},
	}
	for _, tc := range cases {
		if got := IsRetryable(tc.err); got != tc.want {
			t.Fatalf("IsRetryable(%v)=%v want %v", tc.err, got, tc.want)
		}
	}
}
