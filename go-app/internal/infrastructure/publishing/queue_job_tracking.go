package publishing

import (
	"container/list"
	"sync"
)

// JobSnapshot represents a lightweight snapshot of a job for tracking
type JobSnapshot struct {
	ID          string
	Priority    string
	State       string
	TargetName  string
	Fingerprint string
	SubmittedAt int64 // Unix timestamp (seconds)
	StartedAt   *int64
	CompletedAt *int64
	ErrorType   string
	RetryCount  int
}

// JobFilters for querying job tracking store
type JobFilters struct {
	State      string // queued, processing, retrying, succeeded, failed, dlq
	Priority   string // high, medium, low
	TargetName string
	Limit      int
}

// JobTrackingStore defines interface for job tracking
type JobTrackingStore interface {
	// Add stores a job snapshot (evicts LRU if capacity exceeded)
	Add(job *PublishingJob)

	// Get retrieves a job by ID (nil if not found)
	Get(id string) *JobSnapshot

	// List retrieves jobs with optional filtering
	List(filters JobFilters) []*JobSnapshot

	// Remove deletes a job from tracking
	Remove(id string)

	// Clear removes all jobs
	Clear()

	// Size returns current number of tracked jobs
	Size() int
}

// LRUJobTrackingStore implements JobTrackingStore with LRU eviction
type LRUJobTrackingStore struct {
	capacity int
	store    map[string]*list.Element // map[jobID]*Element
	lruList  *list.List               // Doubly-linked list for LRU
	mu       sync.RWMutex
}

// jobTrackingEntry wraps JobSnapshot for LRU list
type jobTrackingEntry struct {
	key   string
	value *JobSnapshot
}

// NewLRUJobTrackingStore creates a new LRU job tracking store
func NewLRUJobTrackingStore(capacity int) *LRUJobTrackingStore {
	if capacity <= 0 {
		capacity = 10000 // Default: track last 10k jobs
	}
	return &LRUJobTrackingStore{
		capacity: capacity,
		store:    make(map[string]*list.Element),
		lruList:  list.New(),
	}
}

// Add stores a job snapshot (evicts LRU if capacity exceeded)
func (s *LRUJobTrackingStore) Add(job *PublishingJob) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create snapshot
	snapshot := &JobSnapshot{
		ID:          job.ID,
		Priority:    job.Priority.String(),
		State:       job.State.String(),
		TargetName:  job.Target.Name,
		Fingerprint: job.EnrichedAlert.Alert.Fingerprint,
		SubmittedAt: job.SubmittedAt.Unix(),
		RetryCount:  job.RetryCount,
	}

	if job.StartedAt != nil {
		ts := job.StartedAt.Unix()
		snapshot.StartedAt = &ts
	}

	if job.CompletedAt != nil {
		ts := job.CompletedAt.Unix()
		snapshot.CompletedAt = &ts
	}

	if job.ErrorType != QueueErrorTypeUnknown {
		snapshot.ErrorType = job.ErrorType.String()
	}

	// Check if already exists
	if elem, ok := s.store[job.ID]; ok {
		// Update existing entry and move to front
		s.lruList.MoveToFront(elem)
		entry := elem.Value.(*jobTrackingEntry)
		entry.value = snapshot
		return
	}

	// Add new entry
	entry := &jobTrackingEntry{key: job.ID, value: snapshot}
	elem := s.lruList.PushFront(entry)
	s.store[job.ID] = elem

	// Evict LRU if capacity exceeded
	if s.lruList.Len() > s.capacity {
		s.evictLRU()
	}
}

// Get retrieves a job by ID (nil if not found)
func (s *LRUJobTrackingStore) Get(id string) *JobSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if elem, ok := s.store[id]; ok {
		// Move to front (most recently used)
		s.lruList.MoveToFront(elem)
		entry := elem.Value.(*jobTrackingEntry)
		return entry.value
	}

	return nil
}

// List retrieves jobs with optional filtering
func (s *LRUJobTrackingStore) List(filters JobFilters) []*JobSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := []*JobSnapshot{}
	count := 0
	limit := filters.Limit
	if limit <= 0 {
		limit = 100 // Default limit
	}

	// Iterate from front to back (most recent first)
	for elem := s.lruList.Front(); elem != nil; elem = elem.Next() {
		if count >= limit {
			break
		}

		entry := elem.Value.(*jobTrackingEntry)
		snapshot := entry.value

		// Apply filters
		if filters.State != "" && snapshot.State != filters.State {
			continue
		}
		if filters.Priority != "" && snapshot.Priority != filters.Priority {
			continue
		}
		if filters.TargetName != "" && snapshot.TargetName != filters.TargetName {
			continue
		}

		results = append(results, snapshot)
		count++
	}

	return results
}

// Remove deletes a job from tracking
func (s *LRUJobTrackingStore) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if elem, ok := s.store[id]; ok {
		s.lruList.Remove(elem)
		delete(s.store, id)
	}
}

// Clear removes all jobs
func (s *LRUJobTrackingStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.store = make(map[string]*list.Element)
	s.lruList = list.New()
}

// Size returns current number of tracked jobs
func (s *LRUJobTrackingStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.lruList.Len()
}

// evictLRU removes the least recently used entry
func (s *LRUJobTrackingStore) evictLRU() {
	// Remove from back (least recently used)
	elem := s.lruList.Back()
	if elem == nil {
		return
	}

	s.lruList.Remove(elem)
	entry := elem.Value.(*jobTrackingEntry)
	delete(s.store, entry.key)
}
