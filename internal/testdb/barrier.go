package testdb

// Barrier is an in-process rendezvous for coordinating two goroutines around a
// critical section — used by migration tests that must observe a state WHILE
// another goroutine holds it (e.g. "attempt apply while an upgrade transaction is
// open and the advisory lock is held").
//
// The blocked party calls Arrive from inside the critical section: it signals its
// arrival (unblocking WaitArrived) and then waits until Release is called. The
// observer calls WaitArrived to know the critical section is entered, performs
// its assertion, then calls Release to let the blocked party proceed. This is a
// pure sync primitive — no subprocess kills, no backend-kill machinery.
type Barrier struct {
	arrived chan struct{}
	release chan struct{}
}

// NewBarrier creates an unarmed barrier.
func NewBarrier() *Barrier {
	return &Barrier{
		arrived: make(chan struct{}),
		release: make(chan struct{}),
	}
}

// Arrive signals that the blocked party has reached the barrier, then blocks
// until Release is called. It must be called exactly once.
func (b *Barrier) Arrive() {
	close(b.arrived)
	<-b.release
}

// WaitArrived blocks until the blocked party has called Arrive.
func (b *Barrier) WaitArrived() {
	<-b.arrived
}

// Release unblocks the party waiting in Arrive. It must be called exactly once.
func (b *Barrier) Release() {
	close(b.release)
}
