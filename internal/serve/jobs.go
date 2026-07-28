package serve

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/smm-h/pgdesign/internal/diagnostic"
)

// auditJobStatus is the lifecycle state of an audit job.
type auditJobStatus string

const (
	auditRunning   auditJobStatus = "running"
	auditDone      auditJobStatus = "done"
	auditFailed    auditJobStatus = "failed"
	auditCancelled auditJobStatus = "cancelled"
)

const (
	// maxConcurrentAuditJobs bounds simultaneously-running audit jobs so a burst of
	// requests cannot spawn unbounded TANE runs against the database.
	maxConcurrentAuditJobs = 4
	// maxRetainedAuditJobs bounds total retained (finished) job records so the map
	// does not grow without limit; the oldest finished jobs are evicted first.
	maxRetainedAuditJobs = 64
	// auditJobHardTimeout bounds any single job's wall-clock time even if the
	// client never cancels it.
	auditJobHardTimeout = 5 * time.Minute
)

// errAuditAtCapacity is returned by start when the concurrent-job bound is hit.
var errAuditAtCapacity = errors.New("audit job manager at capacity")

// auditJob is one audit run. Diagnostics/Error are populated when it finishes.
type auditJob struct {
	ID          string                  `json:"id"`
	Status      auditJobStatus          `json:"status"`
	Diagnostics []map[string]string     `json:"diagnostics,omitempty"`
	Error       string                  `json:"error,omitempty"`
	StartedAt   time.Time               `json:"started_at"`
	FinishedAt  *time.Time              `json:"finished_at,omitempty"`
	cancel      context.CancelFunc      `json:"-"`
	rawDiags    []diagnostic.Diagnostic `json:"-"`
}

// auditJobManager tracks audit jobs with bounded concurrency and retention. It is
// concurrency-safe. The run function is injected so the manager is testable
// without a database.
type auditJobManager struct {
	mu            sync.Mutex
	jobs          map[string]*auditJob
	running       int
	maxConcurrent int
	maxRetained   int
}

func newAuditJobManager() *auditJobManager {
	return &auditJobManager{
		jobs:          make(map[string]*auditJob),
		maxConcurrent: maxConcurrentAuditJobs,
		maxRetained:   maxRetainedAuditJobs,
	}
}

// start launches an audit job running run in a goroutine under a cancellable,
// hard-bounded context. It returns errAuditAtCapacity when the concurrent-job
// bound is reached (no job is created). run's returned diagnostics are stored on
// success; a non-nil error marks the job failed; a context cancellation marks it
// cancelled.
func (m *auditJobManager) start(run func(context.Context) ([]diagnostic.Diagnostic, error)) (*auditJob, error) {
	m.mu.Lock()
	if m.running >= m.maxConcurrent {
		m.mu.Unlock()
		return nil, errAuditAtCapacity
	}
	m.evictLocked()
	ctx, cancel := context.WithTimeout(context.Background(), auditJobHardTimeout)
	job := &auditJob{
		ID:        newJobID(),
		Status:    auditRunning,
		StartedAt: time.Now(),
		cancel:    cancel,
	}
	m.jobs[job.ID] = job
	m.running++
	m.mu.Unlock()

	go func() {
		defer cancel()
		diags, err := run(ctx)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.running--
		now := time.Now()
		job.FinishedAt = &now
		switch {
		case ctx.Err() == context.Canceled:
			job.Status = auditCancelled
		case err != nil:
			job.Status = auditFailed
			job.Error = err.Error()
		default:
			job.Status = auditDone
			job.rawDiags = diags
			job.Diagnostics = diagsToJSON(diags)
		}
	}()
	return job, nil
}

// get returns the job by id (ok=false if unknown). The returned pointer is the
// live record; callers read it under no lock but only after status is terminal,
// or accept a momentary snapshot for status polling.
func (m *auditJobManager) get(id string) (*auditJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	return j, ok
}

// snapshot returns a copy of the job's externally-visible fields, taken under the
// lock, so a poll never races the finishing goroutine's writes.
func (m *auditJobManager) snapshot(id string) (auditJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return auditJob{}, false
	}
	return *j, true
}

// cancel requests cancellation of a running job. It returns ok=false for an
// unknown id. The job transitions to cancelled once its goroutine observes the
// cancelled context.
func (m *auditJobManager) cancel(id string) bool {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	j.cancel()
	return true
}

// evictLocked drops the oldest finished jobs when retention is exceeded. Caller
// must hold m.mu.
func (m *auditJobManager) evictLocked() {
	if len(m.jobs) < m.maxRetained {
		return
	}
	var oldestID string
	var oldest time.Time
	for id, j := range m.jobs {
		if j.Status == auditRunning {
			continue
		}
		if oldestID == "" || j.StartedAt.Before(oldest) {
			oldestID, oldest = id, j.StartedAt
		}
	}
	if oldestID != "" {
		delete(m.jobs, oldestID)
	}
}

// newJobID returns a random 16-hex job identifier.
func newJobID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
