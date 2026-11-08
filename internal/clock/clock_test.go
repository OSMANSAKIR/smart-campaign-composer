package clock

import (
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestFake_AfterFiresOnlyWhenDue(t *testing.T) {
	start := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	f := NewFake(start)
	ch := f.After(time.Second)
	f.Advance(999 * time.Millisecond)
	select {
	case <-ch:
		t.Fatal("fired early")
	default:
	}
	f.Advance(time.Millisecond)
	select {
	case got := <-ch:
		if !got.Equal(start.Add(time.Second)) {
			t.Fatalf("fired at %v", got)
		}
	default:
		t.Fatal("did not fire")
	}
	if f.Waiters() != 0 {
		t.Fatalf("waiters left: %d", f.Waiters())
	}
}
