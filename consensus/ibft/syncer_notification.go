package ibft

// isStaleSyncerNotification reports whether a syncer block-insertion
// notification is for a height that can no longer make the in-progress
// consensus round obsolete.
//
// startConsensus builds `pending` (= its own last known head + 1). The syncer
// notifies it of every block it writes, including ones the node already had.
// Only a notification whose height is at or above `pending` can mean "the
// height I am building just got finalized by someone else" — anything below
// that is stale and must be ignored.
//
// Before this guard existed, the check compared the notified height against
// the *head* at the moment the notification arrived rather than against the
// height actually being built. A late notification for the block a validator
// already has (number == head) passed that check, canceled the in-progress
// round for `pending`, and the very next loop iteration rebuilt the same
// `pending` height again — producing two different signed proposals for one
// height (proven live on testnet: `build block: number=N` logged twice, 2s
// apart, for the same N). Comparing against `pending` instead of `head` fixes
// this without weakening the legitimate case: when the syncer finalizes the
// height a validator is building, the notification's number equals `pending`,
// which still cancels.
func isStaleSyncerNotification(notifiedNumber, pending uint64) bool {
	return notifiedNumber < pending
}

// waitForFreshEvent blocks until the current sequence for `pending` is
// actually done: either it finished on its own (sequenceCh fires), the node
// is shutting down (closeCh fires), or a syncer notification arrives for a
// height at or above `pending`.
//
// This function IS the fix: it is what keeps a stale notification from
// canceling and immediately restarting the same sequence. A stale
// notification is drained and the loop keeps blocking on the very same
// channels — it does not return, so the caller never gets a chance to call
// runSequence(pending) again. Collapsing this back into a single `select`
// (its shape before this fix) reintroduces the bug even if
// isStaleSyncerNotification itself is untouched, which is why this behavior
// is tested directly rather than only through that comparison.
//
// onCancel is invoked only when a fresh notification is the reason for
// returning, never for a stale one and never for the closeCh/sequenceCh
// paths. closed reports whether closeCh fired.
func waitForFreshEvent(
	pending uint64,
	syncerBlockCh <-chan uint64,
	sequenceCh <-chan struct{},
	closeCh <-chan struct{},
	onCancel func(),
) (closed bool) {
	for {
		select {
		case number := <-syncerBlockCh:
			if isStaleSyncerNotification(number, pending) {
				continue
			}

			onCancel()

			return false
		case <-sequenceCh:
			return false
		case <-closeCh:
			return true
		}
	}
}
