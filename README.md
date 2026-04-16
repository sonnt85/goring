# goring

[![Go Reference](https://pkg.go.dev/badge/github.com/sonnt85/goring.svg)](https://pkg.go.dev/github.com/sonnt85/goring)

Generic ring buffer and concurrent worker pool for Go, with blocking/non-blocking push/pop, multi-reader support, and event-driven task dispatch.

## Installation

```bash
go get github.com/sonnt85/goring
```

## Features

- Generic `RingBuffer[T]` — circular buffer implementing `io.ReaderWriter`-style API
- Blocking (`PushWait`, `PopWait`, `ReadWait`, `WriteWait`) and non-blocking (`TryPush`, `TryPop`) operations
- Timeout and context-aware push/pop (`PushWaitTimeOut`, `ReadWaitTimeOut`, `WriteWaitTimeOut`)
- `RingMultipleReader[T]` — lock-free ring buffer supporting multiple independent consumers
- `EventWorker[K]` — concurrent worker pool backed by a `RingBuffer` queue
- `EventWorkerMap[K]` — concurrent worker pool backed by a map (dedup by key)
- Task submission variants: `Submit`, `SubmitForce`, `TrySubmit`, `SubmitWaitDone`, `SubmitWithTimeout`
- Per-task result retrieval, wait-until-finish, and eviction callbacks
- Worker pause/resume and queue enable/disable controls
- Linked-list event queue (`EventLinkedList`)
- Stats reporting for both ring buffer and worker pool

## Usage

```go
// Ring buffer
ring := goring.NewRing[int](64)
ring.PushWait(42)
val := ring.PopWait()

// Worker pool (max 4 workers, buffer 100, task TTL 10s, error TTL 30s)
pool := goring.NewEventWorker[string](4, 100, 10*time.Second, 30*time.Second)
pool.EnableWorker()

task, err := pool.Submit("my-task", 5*time.Second,
    func() string { return "task-id" },
    func(x int) int { return x * 2 }, 21,
)

// Wait for a specific task
retvals, _ := pool.WaitUntilTaskFinishThenDelete("task-id")

// Multiple-reader ring buffer
rmr, _ := goring.NewRingMultipleReader[string](256, 4)
consumer, _ := rmr.NewConsumer()
go rmr.Write("hello")
val2 := consumer.Get()
```

## API

### RingBuffer[T]

- `NewRing[T](size int) *RingBuffer[T]` — create a new ring buffer
- `Push(c T) error` / `PushWait(c T)` / `PushForce(c T)` / `TryPush(c T) error` — write one element
- `Pop() (T, error)` / `PopWait() T` / `TryPop() (T, error)` — read one element
- `Write(p []T)` / `WriteWait(p []T)` / `TryWrite(p []T)` — write slice
- `Read(p []T)` / `ReadWait(p []T)` / `ReadAll()` / `ReadAllWait()` — read slice
- `TryRead(p []T) (n int, err error)` — non-blocking read; returns `ErrAcquireLock` if lock is unavailable
- `TryReadAll() ([]T, error)` — non-blocking read of all available elements
- `PushWaitTimeOut(c T, timeout time.Duration) error` — push with deadline
- `ReadWaitTimeOut(c []T, timeout time.Duration, ctxs ...context.Context)` — read with deadline
- `WaitUntilNotFull()` — block until the buffer has at least one free slot
- `WaitUntilEmpty()` — block until the buffer is completely drained
- `Length() int` / `Capacity() int` / `Free() int` / `IsEmpty() bool` / `IsFull() bool`
- `Reset()` / `Resize()` / `Copy() []T` / `Stats()`

### RingMultipleReader[T]

- `NewRingMultipleReader[T](size, maxConsumers uint32)` — create multi-reader ring
- `Write(value T)` — write (blocks until space is available for all consumers)
- `NewConsumer() (Consumer[T], error)` — create an independent consumer
- `Consumer.Get() T` — blocking read for that consumer
- `Consumer.Remove()` — deregister consumer

### EventWorker[K]

- `NewEventWorker[K](maxWorkers, buffsize int, defaultExpiration, errorTTL time.Duration) *EventWorker[K]`
- `Submit` / `SubmitForce` / `TrySubmit` / `SubmitWithTimeout` / `SubmitWaitDone`
- `GetSavedTask(id K)` / `GetResultTask(id K)` / `WaitUntilTaskFinishThenDelete(id K)`
- `GetSavedTaskThenDelete(id K) (*Task, bool)` — retrieve a saved task and remove it from the store
- `GetResultTaskThenDelete(id K) ([]interface{}, bool, bool)` — retrieve task result values and remove the task
- `GetNumFinishTask() uint32` — return the total number of completed tasks
- `OnEvictedSavedTask(f func(K, *Task))` — register a callback invoked when a saved task expires or is evicted
- `OnEvictedFinishTask(f func(*Task))` — register a callback invoked each time a task finishes successfully
- `Stopped() bool` — return true if the queue is currently empty
- `WaitUntilAllTaskFinish()` / `EnableWorker()` / `DisableWorker()` / `EnableQueue()` / `DisableQueue()`
- `Stats()` / `Size()` / `ConfigMaxWorker(n int)`

### EventWorkerMap[K]

Same API as `EventWorker[K]` but uses a map as backing store (newer submission for same key replaces previous).

### EventLinkedList[T]

A generic ordered list with a movable read cursor, designed for cycling through a fixed set of items.

- `NewEventLinkedList[T]() *EventLinkedList[T]` — create an empty linked list
- `Next() (T, error)` — advance the cursor forward by one and return the element
- `NextWait() (T, error)` — like `Next` but blocks until the list is non-empty
- `Prev() (T, error)` — move the cursor backward by one and return the element
- `PrevWait() (T, error)` — like `Prev` but blocks until the list is non-empty
- `Seek(n int) (T, error)` — move the cursor by `n` steps (positive or negative) and return the element
- `SeekWait(n int) (T, error)` — like `Seek` but blocks until the list is non-empty
- `Current() (T, error)` — return the element at the current cursor position without moving
- `Insert(n int, els ...T) error` — insert elements before position `n`
- `Remove(n int) error` — remove the element at position `n`
- `Copy() ([]T, error)` — return a snapshot copy of the entire list
- `UpdateNewEventLinkedList(p []T) bool` — atomically replace list contents if they differ; broadcasts to waiting goroutines; returns true if changed
- `Length() int` / `Reset()` / `String() string`

## Author

**sonnt85** — [thanhson.rf@gmail.com](mailto:thanhson.rf@gmail.com)

## License

MIT License - see [LICENSE](LICENSE) for details.
