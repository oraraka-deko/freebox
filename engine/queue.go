package engine

import (
	"container/heap"
	"context"
	"errors"
	"sync"
)

// priorityItem wraps a TaskHandle for the priority heap.
type priorityItem struct {
	handle *TaskHandle
	index  int
}

type priorityQueue []*priorityItem

func (pq priorityQueue) Len() int { return len(pq) }
func (pq priorityQueue) Less(i, j int) bool {
	// Higher priority integer runs first
	return pq[i].handle.task.Priority > pq[j].handle.task.Priority
}
func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}
func (pq *priorityQueue) Push(x any) {
	n := len(*pq)
	item := x.(*priorityItem)
	item.index = n
	*pq = append(*pq, item)
}
func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[0 : n-1]
	return item
}

// QueueConfig defines worker pool settings.
type QueueConfig struct {
	MaxWorkers int
}

// DefaultQueueConfig returns default queue settings.
func DefaultQueueConfig() QueueConfig {
	return QueueConfig{
		MaxWorkers: 4,
	}
}

// TaskQueue manages prioritized background worker execution.
type TaskQueue struct {
	config    QueueConfig
	pq        priorityQueue
	mu        sync.Mutex
	cond      *sync.Cond
	closed    bool
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	processor func(h *TaskHandle) error
}

// NewTaskQueue creates and starts a worker queue.
func NewTaskQueue(cfg QueueConfig, processor func(h *TaskHandle) error) *TaskQueue {
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 4
	}
	ctx, cancel := context.WithCancel(context.Background())
	q := &TaskQueue{
		config:    cfg,
		pq:        make(priorityQueue, 0),
		ctx:       ctx,
		cancel:    cancel,
		processor: processor,
	}
	q.cond = sync.NewCond(&q.mu)
	heap.Init(&q.pq)

	for i := 0; i < cfg.MaxWorkers; i++ {
		q.wg.Add(1)
		go q.workerLoop(i)
	}

	return q
}

// Enqueue adds a task handle to the priority queue.
func (q *TaskQueue) Enqueue(handle *TaskHandle) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return errors.New("task queue is closed")
	}

	item := &priorityItem{handle: handle}
	heap.Push(&q.pq, item)
	q.cond.Signal()
	return nil
}

func (q *TaskQueue) workerLoop(id int) {
	defer q.wg.Done()

	for {
		q.mu.Lock()
		for q.pq.Len() == 0 && !q.closed {
			q.cond.Wait()
		}

		if q.closed && q.pq.Len() == 0 {
			q.mu.Unlock()
			return
		}

		item := heap.Pop(&q.pq).(*priorityItem)
		q.mu.Unlock()

		handle := item.handle
		if handle.Status() == StatusCanceled {
			close(handle.doneChan)
			continue
		}

		if q.processor != nil {
			_ = q.processor(handle)
		}
	}
}

// Close gracefully stops the worker queue.
func (q *TaskQueue) Close() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	q.cancel()
	q.cond.Broadcast()
	q.mu.Unlock()

	q.wg.Wait()
}

// Length returns the count of currently pending tasks in queue.
func (q *TaskQueue) Length() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pq.Len()
}
