package common

import (
	"sync/atomic"
	"time"
)

// WriteSyncInterval is the most often a database write waits for the disk.
const WriteSyncInterval = time.Second

// WriteSyncThrottle picks which database writes are flushed to disk (fsync)
// before they return. Without it nothing is flushed, and a power loss can
// keep a newer block in one database and lose its state in the other (audit
// BI-M1). A flush costs a few milliseconds: at the chain head, one block every
// two seconds, every block write is flushed; while syncing old blocks, at
// most one write a second is. A flushed write also makes every earlier write
// to the same database durable.
type WriteSyncThrottle struct {
	last atomic.Int64
}

// ShouldSync reports whether the write about to happen should be flushed.
// A nil throttle never flushes.
func (t *WriteSyncThrottle) ShouldSync() bool {
	if t == nil {
		return false
	}

	now := time.Now().UnixNano()
	last := t.last.Load()

	if last != 0 && now-last < int64(WriteSyncInterval) {
		return false
	}

	return t.last.CompareAndSwap(last, now)
}
