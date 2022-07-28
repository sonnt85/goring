package goring

import (
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"runtime"

	"github.com/sonnt85/goramcache"
	"github.com/sonnt85/gosutils/funcmap"
	"github.com/sonnt85/gosyncutils"
	"golang.org/x/exp/constraints"
)

// EventWorkerMap is a collection of goroutines, where the number of concurrent
// goroutines processing requests does not exceed the specified maximum.
type EventWorkerMap[K constraints.Ordered] struct {
	maxWorkers       int
	numFinishTasks   uint32
	numErrorTasks    uint32
	mapbuffsize      uint32
	qm               *goramcache.Cache[K, *funcmap.Task[K]]
	pausePushQueue   *gosyncutils.EventOpject[bool]
	pauseWorker      *gosyncutils.EventOpject[bool]
	savedTasks       *goramcache.Cache[K, *funcmap.Task[K]]
	numWorkerRunning *gosyncutils.EventOpject[int]
	onFinishTask     func(*funcmap.Task[K])
	brocastSubmit    *gosyncutils.EventOpject[struct{}]
	sync.RWMutex
}

// New creates and starts a pool of worker goroutines.
// The maxWorkers parameter specifies the maximum number of workers that can
// execute tasks concurrently.
func NewEventWorkerMap[K constraints.Ordered](maxWorkers int, mapbuffsize int, defaultExpiration, errorAllowTimeExpiration time.Duration) *EventWorkerMap[K] {
	// There must be at least one worker.
	if maxWorkers < 1 {
		maxWorkers = runtime.NumCPU()
	}

	pool := &EventWorkerMap[K]{
		maxWorkers:       maxWorkers,
		pausePushQueue:   gosyncutils.NewEventOpject[bool](),
		numWorkerRunning: gosyncutils.NewEventOpject[int](),
		pauseWorker:      gosyncutils.NewEventOpject[bool](),
		savedTasks:       goramcache.NewCache[K, *funcmap.Task[K]](defaultExpiration, errorAllowTimeExpiration),
		qm:               goramcache.NewCache[K, *funcmap.Task[K]](0, 0),
		mapbuffsize:      uint32(mapbuffsize),
		brocastSubmit:    gosyncutils.NewEventOpject[struct{}](),
	}
	pool.pauseWorker.Set(true)
	// Start the task dispatcher.
	go pool.dispatch()

	return pool
}

func (ew *EventWorkerMap[K]) OnEvictedSavedTask(f func(K, *funcmap.Task[K])) {
	ew.savedTasks.OnEvicted(f)
}

func (ew *EventWorkerMap[K]) OnEvictedFinishTask(f func(*funcmap.Task[K])) {
	ew.onFinishTask = f
}

// Size returns the maximum number of concurrent workers.
func (ew *EventWorkerMap[K]) Size() int {
	ew.RLock()
	defer ew.RUnlock()
	return ew.maxWorkers
}

func (ew *EventWorkerMap[K]) ConfigMaxWorker(n int) {
	ew.Lock()
	ew.maxWorkers = n
	ew.Unlock()
	return
}

// Stopped returns true if this worker pool has been stopped.
func (ew *EventWorkerMap[K]) Stats() (retmap map[string]interface{}) {
	retmap = make(map[string]interface{}, 0)
	retmap["Maximum number of workers"] = ew.maxWorkers
	retmap["Workers are pause"] = ew.pauseWorker.Get()
	retmap["Queue capacity"] = atomic.LoadUint32(&ew.mapbuffsize)
	retmap["Queue length"] = ew.qm.Length()
	retmap["Queue is pause"] = ew.pausePushQueue.Get()
	retmap["Number of active workers"] = ew.numWorkerRunning.Get()
	retmap["Number of tasks completed"] = atomic.LoadUint32(&ew.numFinishTasks)
	retmap["Number tasks saved"] = ew.savedTasks.Length()
	retmap["Number tasks can not run"] = atomic.LoadUint32(&ew.numErrorTasks)
	return retmap
}

// Stopped returns true if this worker pool has been stopped.
func (ew *EventWorkerMap[K]) Stopped() bool {
	return ew.qm.Length() == 0
}

// Submit enqueues a function for a worker to execute.
func (ew *EventWorkerMap[K]) Submit(taskname string, expirationSaveTask time.Duration, fgid func() K, f interface{}, params ...interface{}) (task *funcmap.Task[K], err error) {
	if ew.pausePushQueue.Get() {
		return task, ErrPausePush
	}
	task, err = funcmap.NewTask[K](taskname, fgid, f, params...)
	if err == nil {
		ew.qm.SetDefault(task.Id, task)
		if expirationSaveTask >= 0 {
			ew.savedTasks.Set(task.Id, task, expirationSaveTask)
		}
		ew.brocastSubmit.SendBroacast()
	}
	return
}

// Submit force enqueues a function for a worker to execute (remove first element if is full).
func (ew *EventWorkerMap[K]) SubmitForce(taskname string, expirationSaveTask time.Duration, fgid func() K, f interface{},
	params ...interface{}) (task *funcmap.Task[K], err error) {
	if ew.pausePushQueue.Get() {
		return task, ErrPausePush
	}
	task, err = funcmap.NewTask[K](taskname, fgid, f, params...)
	if err == nil {
		ew.qm.SetDefault(task.Id, task)
		if expirationSaveTask >= 0 {
			ew.savedTasks.Set(task.Id, task, expirationSaveTask)
		}
		ew.brocastSubmit.SendBroacast()
	}
	return
}

// Submit with timeout enqueues a function for a worker to execute, not recomment
func (ew *EventWorkerMap[K]) SubmitWithTimeout(taskname string, expirationSaveTask time.Duration, fgid func() K, f interface{},
	timeout time.Duration, params ...interface{}) (task *funcmap.Task[K], err error) {
	if ew.pausePushQueue.Get() {
		return task, ErrPausePush
	}
	task, err = funcmap.NewTask[K](taskname, fgid, f, params...)
	if err == nil {
		ew.qm.SetDefault(task.Id, task)

		// err = ew.q.PushWaitTimeOut(task, timeout)
		// if err == nil {
		if expirationSaveTask >= 0 {
			ew.savedTasks.Set(task.Id, task, expirationSaveTask)
		}
		ew.brocastSubmit.SendBroacast()
		// }
	}
	return
}

// Submit enqueues a function for a worker to execute, no waitting for finish
func (ew *EventWorkerMap[K]) TrySubmit(taskname string, expirationSaveTask time.Duration, fgid func() K, f interface{},
	params ...interface{}) (task *funcmap.Task[K], err error) {
	if ew.pausePushQueue.Get() {
		return task, ErrPausePush
	}
	task, err = funcmap.NewTask[K](taskname, fgid, f, params...)
	if err == nil {
		ew.qm.SetDefault(task.Id, task)
		// err = ew.q.TryPush(task)
		if expirationSaveTask >= 0 {
			ew.savedTasks.Set(task.Id, task, expirationSaveTask)
		}
		ew.brocastSubmit.SendBroacast()
	}
	return
}

func (ew *EventWorkerMap[K]) GetSavedTask(id K) (*funcmap.Task[K], bool) {
	return ew.savedTasks.Get(id)
}

func (ew *EventWorkerMap[K]) GetSavedTaskThenDelete(id K) (*funcmap.Task[K], bool) {
	return ew.savedTasks.GetThenDelete(id)
}

func (ew *EventWorkerMap[K]) WaitUntilTaskFinishThenDelete(id K) (retvals []reflect.Value, hastask bool) {
	var task *funcmap.Task[K]
	task, hastask = ew.savedTasks.Get(id)
	if hastask {
		retvals = task.WaitTaskFinishThenGetValues()
		ew.savedTasks.Delete(id)
	}
	return
}

func (ew *EventWorkerMap[K]) WaitUntilAllTaskFinish() []*funcmap.Task[K] {
	ew.numWorkerRunning.WaitUntil(func(cnt int) bool {
		return ew.qm.Length() == 0
	})
	return ew.savedTasks.Values()
}

func (ew *EventWorkerMap[K]) GetResultTask(id K) (retvals []reflect.Value, isDone bool, hastask bool) {
	var task *funcmap.Task[K]
	task, hastask = ew.savedTasks.Get(id)
	if hastask {
		retvals, isDone = task.GetRetValues()
	}
	return
}

func (ew *EventWorkerMap[K]) GetResultTaskThenDelete(id K) (retvals []reflect.Value, isDone bool, hastask bool) {
	var task *funcmap.Task[K]
	task, hastask = ew.savedTasks.GetThenDelete(id)
	if hastask {
		retvals, isDone = task.GetRetValues()
	}
	return
}

// SubmitWait enqueues the given function and waits for it to be executed, get return values
func (ew *EventWorkerMap[K]) SubmitWaitDone(taskname string, fgid func() K, f interface{}, params ...interface{}) (retvals []reflect.Value, err error) {
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

func (ew *EventWorkerMap[K]) GetNumFinishTask() uint32 {
	ew.RLock()
	defer ew.RUnlock()
	return ew.numFinishTasks
}

//Stop push task to queue
func (ew *EventWorkerMap[K]) DisableQueue() {
	ew.pausePushQueue.Set(true)
}

//Enable push task to queue
func (ew *EventWorkerMap[K]) EnableWorker() {
	ew.pauseWorker.SetThenSendBroadcast(false)
}

//Stop push task to queue
func (ew *EventWorkerMap[K]) DisableWorker() {
	ew.pauseWorker.Set(true)
}

//Enable push task to queue
func (ew *EventWorkerMap[K]) EnableQueue() {
	ew.pausePushQueue.SetThenSendBroadcast(false)
}

// dispatch sends the next queued task to an available worker.
func (ew *EventWorkerMap[K]) dispatch() {
	//
	// var key K
	// var ok bool
	for {
		ew.pauseWorker.WaitWhile(func(b bool) bool {
			return b
		})
		var task = new(funcmap.Task[K])
		var ok bool
		ew.brocastSubmit.WaitWhile(func(b struct{}) bool {
			_, task, ok = ew.qm.GetRandomThenDelete()
			return !ok
		})

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
