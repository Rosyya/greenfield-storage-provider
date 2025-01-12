package gfsptqueue

import (
	"errors"
	"sync"

	corercmgr "github.com/bnb-chain/greenfield-storage-provider/core/rcmgr"
	coretask "github.com/bnb-chain/greenfield-storage-provider/core/task"
	"github.com/bnb-chain/greenfield-storage-provider/core/taskqueue"
	"github.com/bnb-chain/greenfield-storage-provider/pkg/log"
)

var _ taskqueue.TQueueWithLimit = &GfSpTQueueWithLimit{}
var _ taskqueue.TQueueOnStrategyWithLimit = &GfSpTQueueWithLimit{}

type GfSpTQueueWithLimit struct {
	name    string
	tasks   []coretask.Task
	indexer map[coretask.TKey]int
	cap     int
	mux     sync.RWMutex

	gcFunc     func(task2 coretask.Task) bool
	filterFunc func(task2 coretask.Task) bool
}

func NewGfSpTQueueWithLimit(name string, cap int) taskqueue.TQueueOnStrategyWithLimit {
	return &GfSpTQueueWithLimit{
		name:    name,
		cap:     cap,
		tasks:   make([]coretask.Task, 0),
		indexer: make(map[coretask.TKey]int),
	}
}

// Len returns the length of queue.
func (t *GfSpTQueueWithLimit) Len() int {
	t.mux.RLock()
	defer t.mux.RUnlock()
	return len(t.tasks)
}

// Cap returns the capacity of queue.
func (t *GfSpTQueueWithLimit) Cap() int {
	return t.cap
}

// Has returns an indicator whether the task in queue.
func (t *GfSpTQueueWithLimit) Has(key coretask.TKey) bool {
	t.mux.RLock()
	defer t.mux.RUnlock()
	_, ok := t.indexer[key]
	return ok
}

func (t *GfSpTQueueWithLimit) TopByLimit(limit corercmgr.Limit) coretask.Task {
	t.mux.RLock()
	defer t.mux.RUnlock()
	if len(t.tasks) == 0 {
		return nil
	}
	for i := len(t.tasks) - 1; i >= 0; i-- {
		task := t.tasks[i]
		if limit.NotLess(task.EstimateLimit()) {
			if t.filterFunc == nil {
				return task
			}
			if t.filterFunc(task) {
				return task
			}
		}
	}
	return nil
}

// PopByLimit pops and returns the top task that the LimitEstimate less than the param in the queue.
func (t *GfSpTQueueWithLimit) PopByLimit(limit corercmgr.Limit) coretask.Task {
	t.mux.Lock()
	var task coretask.Task
	defer func() {
		if task != nil {
			t.delete(task)
		}
		t.mux.Unlock()
	}()
	if len(t.tasks) == 0 {
		return nil
	}
	for i := len(t.tasks) - 1; i >= 0; i-- {
		task = t.tasks[i]
		if limit.NotLess(task.EstimateLimit()) {
			if t.filterFunc == nil {
				return task
			}
			if t.filterFunc(task) {
				return task
			}
		}
	}
	return nil
}

// PopByKey pops the task by the task key, if the task does not exist , returns nil.
func (t *GfSpTQueueWithLimit) PopByKey(key coretask.TKey) coretask.Task {
	t.mux.Lock()
	defer t.mux.Unlock()
	idx, ok := t.indexer[key]
	if !ok {
		return nil
	}
	if idx >= len(t.tasks) {
		log.Errorw("[BUG] index out of bounds", "queue", t.name,
			"len", len(t.tasks), "index", idx)
		return nil
	}
	task := t.tasks[idx]
	t.delete(task)
	return task
}

// Push pushes the task in queue tail, if the queue len greater the capacity, returns error.
func (t *GfSpTQueueWithLimit) Push(task coretask.Task) error {
	t.mux.Lock()
	defer t.mux.Unlock()
	if _, ok := t.indexer[task.Key()]; ok {
		log.Warnw("push repeat task", "queue", t.name, "task", task.Key())
		return errors.New("repeated task")
	}
	if t.exceed() {
		clear := false
		if t.gcFunc != nil {
			for _, backup := range t.tasks {
				if t.gcFunc(backup) {
					t.delete(task)
					clear = true
				}
			}
		}
		if !clear {
			log.Warnw("queue exceed", "queue", t.name, "cap", t.cap, "len", len(t.tasks))
			return errors.New("queue exceed")
		}
	}
	t.add(task)
	return nil
}

func (t *GfSpTQueueWithLimit) exceed() bool {
	return len(t.tasks) >= t.cap
}

func (t *GfSpTQueueWithLimit) add(task coretask.Task) {
	t.tasks = append(t.tasks, task)
	t.indexer[task.Key()] = len(t.tasks) - 1
}

func (t *GfSpTQueueWithLimit) delete(task coretask.Task) {
	idx, ok := t.indexer[task.Key()]
	if !ok {
		return
	}
	if idx >= len(t.tasks) {
		log.Errorw("[BUG] index out of bounds", "queue", t.name,
			"len", len(t.tasks), "index", idx)
		return
	}
	t.tasks = append(t.tasks[0:idx], t.tasks[idx+1:]...)
	delete(t.indexer, task.Key())
}

// SetFilterTaskStrategy sets the callback func to filter task for popping or topping.
func (t *GfSpTQueueWithLimit) SetFilterTaskStrategy(filter func(coretask.Task) bool) {
	t.mux.Lock()
	defer t.mux.Unlock()
	t.filterFunc = filter
}

// SetRetireTaskStrategy sets the callback func to retire task, when the queue is full, it will be
// called to retire tasks.
func (t *GfSpTQueueWithLimit) SetRetireTaskStrategy(retire func(coretask.Task) bool) {
	t.mux.Lock()
	defer t.mux.Unlock()
	t.gcFunc = retire
}

// ScanTask scans all tasks, and call the func one by one task.
func (t *GfSpTQueueWithLimit) ScanTask(scan func(coretask.Task)) {
	t.mux.RLock()
	defer t.mux.RUnlock()
	for _, task := range t.tasks {
		scan(task)
	}
}
