package goring

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"runtime"

	"github.com/sonnt85/goramcache"
	"github.com/sonnt85/gosutils/funcmap"
	"github.com/sonnt85/gosyncutils"
	"golang.org/x/exp/constraints"
)

// EventWorker is a collection of goroutines, where the number of concurrent
// goroutines processing requests does not exceed the specified maximum.
type EventWorker[K constraints.Ordered] struct {
	maxWorkers       int
	numFinishTasks   uint32
	numErrorTasks    uint32
	q                *RingBuffer[*funcmap.Task[K]]
	pausePushQueue   *gosyncutils.EventOpject[bool]
	pauseWorker      *gosyncutils.EventOpject[bool]
	savedTasks       *goramcache.Cache[K, *funcmap.Task[K]]
	numWorkerRunning *gosyncutils.EventOpject[int]
	onFinishTask     func(*funcmap.Task[K])

	sync.RWMutex
}

var (
	ErrPausePush = fmt.Errorf("queue is pause")
)

// New creates and starts a pool of worker goroutines.
// The maxWorkers parameter specifies the maximum number of workers that can
// execute tasks concurrently.
func NewEventWorker[K constraints.Ordered](maxWorkers int, buffsize int, defaultExpiration, errorAllowTimeExpiration time.Duration) *EventWorker[K] {
	// There must be at least one worker.
	if maxWorkers < 1 {
		maxWorkers = runtime.NumCPU()
	}

	pool := &EventWorker[K]{
		maxWorkers:       maxWorkers,
		q:                NewRing[*funcmap.Task[K]](buffsize),
		pausePushQueue:   gosyncutils.NewEventOpject[bool](),
		numWorkerRunning: gosyncutils.NewEventOpject[int](),
		pauseWorker:      gosyncutils.NewEventOpject[bool](),
		savedTasks:       goramcache.NewCache[K, *funcmap.Task[K]](defaultExpiration, errorAllowTimeExpiration),
	}
	pool.pauseWorker.Set(true)
	// Start the task dispatcher.
	go pool.dispatch()

	return pool
}

func (ew *EventWorker[K]) OnEvictedSavedTask(f func(K, *funcmap.Task[K])) {
	f1 := func(k K, t *funcmap.Task[K]) {
		t.SetIgnore(true)
		f(k, t)
	}
	ew.savedTasks.OnEvicted(f1)
}

func (ew *EventWorker[K]) OnEvictedFinishTask(f func(*funcmap.Task[K])) {
	ew.onFinishTask = f
}

// Size returns the maximum number of concurrent workers.
func (ew *EventWorker[K]) Size() int {
	ew.RLock()
	defer ew.RUnlock()
	return ew.maxWorkers
}

func (ew *EventWorker[K]) ConfigMaxWorker(n int) {
	ew.Lock()
	ew.maxWorkers = n
	ew.Unlock()
}

// Stopped returns true if this worker pool has been stopped.
func (ew *EventWorker[K]) Stats() (retmap map[string]interface{}) {
	retmap = make(map[string]interface{}, 0)
	retmap["Maximum number of workers"] = ew.maxWorkers
	retmap["Workers are pause"] = ew.pauseWorker.Get()
	retmap["Queue capacity"] = ew.q.Capacity()
	retmap["Queue length"] = ew.q.Length()
	retmap["Queue is pause"] = ew.pausePushQueue.Get()
	retmap["Number of active workers"] = ew.numWorkerRunning.Get()
	retmap["Number of tasks completed"] = atomic.LoadUint32(&ew.numFinishTasks)
	retmap["Number tasks saved"] = ew.savedTasks.Length()
	retmap["Number tasks can not run"] = atomic.LoadUint32(&ew.numErrorTasks)
	return retmap
}

// Stopped returns true if this worker pool has been stopped.
func (ew *EventWorker[K]) Stopped() bool {
	return ew.q.IsEmpty()
}

// Submit enqueues a function for a worker to execute.
func (ew *EventWorker[K]) Submit(taskname string, expirationSaveTask time.Duration, fgid func() K, f interface{}, params ...interface{}) (task *funcmap.Task[K], err error) {
	if ew.pausePushQueue.Get() {
		return task, ErrPausePush
	}
	task, err = funcmap.NewTask(taskname, fgid, f, params...)
	if err == nil {
		ew.q.PushWait(task)
		if expirationSaveTask >= 0 {
			ew.savedTasks.Set(task.Id, task, expirationSaveTask)
		}
	}
	return
}

// Submit force enqueues a function for a worker to execute (remove first element if is full).
func (ew *EventWorker[K]) SubmitForce(taskname string, expirationSaveTask time.Duration, fgid func() K, f interface{},
	params ...interface{}) (task *funcmap.Task[K], err error) {
	if ew.pausePushQueue.Get() {
		return task, ErrPausePush
	}
	task, err = funcmap.NewTask(taskname, fgid, f, params...)
	if err == nil {
		ew.q.PushForce(task)
		if expirationSaveTask >= 0 {
			ew.savedTasks.Set(task.Id, task, expirationSaveTask)
		}
	}
	return
}

// Submit with timeout enqueues a function for a worker to execute, not recomment
func (ew *EventWorker[K]) SubmitWithTimeout(taskname string, expirationSaveTask time.Duration, fgid func() K, f interface{},
	timeout time.Duration, params ...interface{}) (task *funcmap.Task[K], err error) {
	if ew.pausePushQueue.Get() {
		return task, ErrPausePush
	}
	task, err = funcmap.NewTask(taskname, fgid, f, params...)
	if err == nil {
		err = ew.q.PushWaitTimeOut(task, timeout)
		if err == nil {
			if expirationSaveTask >= 0 {

				ew.savedTasks.Set(task.Id, task, expirationSaveTask)
			}
		}
	}
	return
}

// Submit enqueues a function for a worker to execute, no waitting for finish
func (ew *EventWorker[K]) TrySubmit(taskname string, expirationSaveTask time.Duration, fgid func() K, f interface{},
	params ...interface{}) (task *funcmap.Task[K], err error) {
	if ew.pausePushQueue.Get() {
		return task, ErrPausePush
	}
	task, err = funcmap.NewTask(taskname, fgid, f, params...)
	if err == nil {
		err = ew.q.TryPush(task)
		if expirationSaveTask >= 0 {

			ew.savedTasks.Set(task.Id, task, expirationSaveTask)
		}
	}
	return
}

func (ew *EventWorker[K]) GetSavedTask(id K) (*funcmap.Task[K], bool) {
	return ew.savedTasks.Get(id)
}

func (ew *EventWorker[K]) GetSavedTaskThenDelete(id K) (*funcmap.Task[K], bool) {
	return ew.savedTasks.GetThenDelete(id)
}

func (ew *EventWorker[K]) WaitUntilTaskFinishThenDelete(id K) (retvals []interface{}, hastask bool) {
	var task *funcmap.Task[K]
	task, hastask = ew.savedTasks.Get(id)
	if hastask {
		retvals = task.WaitTaskFinishThenGetValues()
		ew.savedTasks.Delete(id)
	}
	return
}

func (ew *EventWorker[K]) WaitUntilAllTaskFinish() []*funcmap.Task[K] {
	ew.numWorkerRunning.WaitUntil(func(cnt int) bool {
		return ew.q.Length() == 0
	})
	return ew.savedTasks.Values()
}

func (ew *EventWorker[K]) GetResultTask(id K) (retvals []interface{}, isDone bool, hastask bool) {
	var task *funcmap.Task[K]
	task, hastask = ew.savedTasks.Get(id)
	if hastask {
		retvals, isDone = task.GetRetValues()
	}
	return
}

func (ew *EventWorker[K]) GetResultTaskThenDelete(id K) (retvals []interface{}, isDone bool, hastask bool) {
	var task *funcmap.Task[K]
	task, hastask = ew.savedTasks.GetThenDelete(id)
	if hastask {
		retvals, isDone = task.GetRetValues()
	}
	return
}

// SubmitWait enqueues the given function and waits for it to be executed, get return values
func (ew *EventWorker[K]) SubmitWaitDone(taskname string, fgid func() K, f interface{}, params ...interface{}) (retvals []interface{}, err error) {
	var task *funcmap.Task[K]
	if ew.pausePushQueue.Get() {
		return retvals, ErrPausePush
	}
	task, err = ew.Submit(taskname, -1, fgid, f, params...)
	if err != nil {
		return
	}
	ew.numWorkerRunning.WaitUntil(func(int) bool {
		return task.IsFinish()
	})
	retvals, _ = task.GetRetValues()
	return
}

func (ew *EventWorker[K]) GetNumFinishTask() uint32 {
	ew.RLock()
	defer ew.RUnlock()
	return ew.numFinishTasks
}

// Stop push task to queue
func (ew *EventWorker[K]) DisableQueue() {
	ew.pausePushQueue.Set(true)
}

// Enable push task to queue
func (ew *EventWorker[K]) EnableWorker() {
	ew.pauseWorker.SetThenSendBroadcast(false)
}

// Stop push task to queue
func (ew *EventWorker[K]) DisableWorker() {
	ew.pauseWorker.Set(true)
}

// Enable push task to queue
func (ew *EventWorker[K]) EnableQueue() {
	ew.pausePushQueue.SetThenSendBroadcast(false)
}

// dispatch sends the next queued task to an available worker.
func (ew *EventWorker[K]) dispatch() {

	for {
		ew.pauseWorker.WaitWhile(func(b bool) bool {
			return b
		})
		var task = new(funcmap.Task[K])
		task = ew.q.PopWait()
		if task.IsIgnore() {
			continue
		}
		ew.numWorkerRunning.WaitUntil(func(cnt int) bool {
			return cnt < ew.maxWorkers
		})

		go func() {
			var err error
			defer func() {
				if err != nil {
					atomic.AddUint32(&ew.numErrorTasks, 1)
				}
				atomic.AddUint32(&ew.numFinishTasks, 1)
			}()
			_, err = task.Call()
			if err == nil {
				if ew.onFinishTask != nil {
					ew.onFinishTask(task)
				}
			}
			ew.numWorkerRunning.EditThenSendBroadcast(func(cnt int) int { //has new finish
				cnt--
				return cnt
			})
		}()
		ew.numWorkerRunning.Edit(func(cnt int) int {
			cnt++
			return cnt
		})
	}
}
