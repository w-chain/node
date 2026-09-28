package ibft

import (
	"testing"
	"time"
)

// Reproduces the exact scenario from the bug: head is 100, the validator is
// building 101 (pending=101), and a late notification arrives for the block
// it already has (100). That must be ignored, not treated as fresh.
func TestIsStaleSyncerNotification(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		notifiedNumber uint64
		pending        uint64
		wantStale      bool
	}{
		{"notification for the block already had (the reported bug)", 100, 101, true},
		{"notification further behind", 50, 101, true},
		{"syncer finalizes exactly the height being built", 101, 101, false},
		{"syncer jumps ahead of the height being built", 105, 101, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := isStaleSyncerNotification(tc.notifiedNumber, tc.pending); got != tc.wantStale {
				t.Fatalf("isStaleSyncerNotification(%d, %d) = %v, want %v",
					tc.notifiedNumber, tc.pending, got, tc.wantStale)
			}
		})
	}
}

const testTimeout = 2 * time.Second

// This is the test the comparison alone can't give: it exercises the actual
// blocking behavior in startConsensus's wait loop, not just the yes/no
// decision. A stale notification must not make waitForFreshEvent return at
// all — if it did, the caller would loop back and call runSequence(pending)
// again, which is the exact bug (two signed proposals for one height). If
// this ever regresses back to a single `select` (its shape before the fix),
// this test fails even though isStaleSyncerNotification is untouched.
func TestWaitForFreshEvent_StaleNotificationsDoNotReturn(t *testing.T) {
	t.Parallel()

	const pending = uint64(101)

	syncerBlockCh := make(chan uint64)
	sequenceCh := make(chan struct{})
	closeCh := make(chan struct{})

	cancelCalls := 0
	done := make(chan bool, 1)

	go func() {
		done <- waitForFreshEvent(pending, syncerBlockCh, sequenceCh, closeCh, func() {
			cancelCalls++
		})
	}()

	// Send several stale notifications, including the reported bug's exact
	// value (100, the block already held). None of these may cause the
	// function to return: doing so would let the caller rebuild `pending`.
	for _, stale := range []uint64{100, 0, 99, 100} {
		select {
		case syncerBlockCh <- stale:
		case <-time.After(testTimeout):
			t.Fatalf("waitForFreshEvent did not accept stale notification %d — it may have returned early", stale)
		}

		// It must still be blocked, not having returned for that stale value.
		select {
		case closed := <-done:
			t.Fatalf("waitForFreshEvent returned (closed=%v) after a stale notification (%d) — "+
				"this is the exact regression: it would let the caller restart runSequence(%d)",
				closed, stale, pending)
		case <-time.After(50 * time.Millisecond):
		}
	}

	if cancelCalls != 0 {
		t.Fatalf("onCancel was called %d times for stale notifications, want 0", cancelCalls)
	}

	// Now the sequence finishes on its own; the function must return promptly
	// without ever having invoked onCancel.
	close(sequenceCh)

	select {
	case closed := <-done:
		if closed {
			t.Fatal("waitForFreshEvent reported closed=true on sequence completion, want false")
		}
	case <-time.After(testTimeout):
		t.Fatal("waitForFreshEvent did not return after sequenceCh fired")
	}

	if cancelCalls != 0 {
		t.Fatalf("onCancel was called %d times, want 0 (sequence finished on its own)", cancelCalls)
	}
}

// The legitimate case: a notification at or above `pending` must cancel and
// return immediately (a validator catching up, or the syncer finalizing
// exactly the height being built, must not be ignored).
func TestWaitForFreshEvent_FreshNotificationCancels(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		notified uint64
	}{
		{"exactly pending", 101},
		{"syncer ahead of pending", 150},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const pending = uint64(101)

			syncerBlockCh := make(chan uint64, 1)
			sequenceCh := make(chan struct{})
			closeCh := make(chan struct{})

			cancelCalls := 0
			done := make(chan bool, 1)

			go func() {
				done <- waitForFreshEvent(pending, syncerBlockCh, sequenceCh, closeCh, func() {
					cancelCalls++
				})
			}()

			syncerBlockCh <- tc.notified

			select {
			case closed := <-done:
				if closed {
					t.Fatal("closed=true, want false")
				}
			case <-time.After(testTimeout):
				t.Fatal("waitForFreshEvent did not return for a fresh notification")
			}

			if cancelCalls != 1 {
				t.Fatalf("onCancel called %d times, want exactly 1", cancelCalls)
			}
		})
	}
}

// Shutdown must win even mid-wait, and must not invoke onCancel (the caller
// handles stopSequence itself on the closed path).
func TestWaitForFreshEvent_CloseChStopsWaiting(t *testing.T) {
	t.Parallel()

	syncerBlockCh := make(chan uint64)
	sequenceCh := make(chan struct{})
	closeCh := make(chan struct{})

	cancelCalls := 0
	done := make(chan bool, 1)

	go func() {
		done <- waitForFreshEvent(101, syncerBlockCh, sequenceCh, closeCh, func() {
			cancelCalls++
		})
	}()

	close(closeCh)

	select {
	case closed := <-done:
		if !closed {
			t.Fatal("closed=false, want true")
		}
	case <-time.After(testTimeout):
		t.Fatal("waitForFreshEvent did not return after closeCh fired")
	}

	if cancelCalls != 0 {
		t.Fatalf("onCancel called %d times on shutdown, want 0", cancelCalls)
	}
}
