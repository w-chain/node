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
