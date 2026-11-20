package cronlock

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRunIsExclusiveAndReleases(t *testing.T) {
	mr := miniredis.RunT(t)
	l := New(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	ctx := context.Background()

	inner := false
	ran, err := l.Run(ctx, "job", time.Minute, func(context.Context) {
		// A second replica firing while the first still runs must skip.
		var err error
		inner, err = l.Run(ctx, "job", time.Minute, func(context.Context) {})
		if err != nil {
			t.Error(err)
		}
	})
	if err != nil || !ran {
		t.Fatalf("first run: ran=%v err=%v", ran, err)
	}
	if inner {
		t.Fatal("concurrent run should have been skipped")
	}
	if mr.Exists(keyPrefix + "job") {
		t.Fatal("lease should be released after the job")
	}
	if ran, _ := l.Run(ctx, "job", time.Minute, func(context.Context) {}); !ran {
		t.Fatal("next run should acquire the released lease")
	}
}

func TestReleaseDoesNotDropForeignLease(t *testing.T) {
	mr := miniredis.RunT(t)
	l := New(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	_, _ = l.Run(context.Background(), "job", time.Minute, func(context.Context) {
		// Our lease expired and another replica took it; our release must not delete theirs.
		mr.Set(keyPrefix+"job", "someone-else")
	})
	if v, _ := mr.Get(keyPrefix + "job"); v != "someone-else" {
		t.Fatalf("foreign lease was released: %q", v)
	}
}

func TestNilLockerRunsUnguarded(t *testing.T) {
	var l *Locker
	called := false
	ran, err := l.Run(context.Background(), "job", time.Second, func(context.Context) { called = true })
	if err != nil || !ran || !called {
		t.Fatalf("ran=%v called=%v err=%v", ran, called, err)
	}
}
