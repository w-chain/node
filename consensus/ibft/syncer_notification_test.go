package ibft

import "testing"

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
