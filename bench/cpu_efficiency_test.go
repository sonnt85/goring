// CPU-efficiency benchmarks — these measure *system-level* throughput on a
// constrained CPU. The scenario: ring workers run alongside an independent
// "useful work" goroutine doing real computation. If a ring implementation
// parks blocked goroutines (goring, channels), the CPU freed up flows to the
// useful-work goroutine, and the *combined* throughput rises. If the ring
// spins on CAS while blocked (Workiva), it starves useful-work and combined
// throughput drops.
//
// We cap GOMAXPROCS below the worker count to force contention, then measure
// how many useful-work iterations complete during a fixed benchmark window.
//
// Usage: GOMAXPROCS=2 go test -bench=CPUEfficiency -benchtime=3s
package bench

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gds "github.com/Workiva/go-datastructures/queue"
	"github.com/sonnt85/goring"
)

// cpuWork performs a small, non-trivial computation. Returns result so the
// compiler doesn't eliminate it.
//
//go:noinline
func cpuWork(seed uint64) uint64 {
	x := seed
	for i := 0; i < 200; i++ {
		x = x*6364136223846793005 + 1442695040888963407
		x ^= x >> 33
	}
	return x
}

// benchWorkWhileRingBusy runs 1 producer + 1 slow consumer on the ring,
// AND a separate "useful work" goroutine. It reports useful-work iterations
// per second (higher = more CPU-efficient ring implementation).
func benchWorkWhileRingBusy(
	b *testing.B,
	produce func(int),
	consume func() int,
	cleanup func(),
) {
	// Keep the benchmark short but measurable.
	const duration = 1500 * time.Millisecond
	var workDone atomic.Uint64
	var stopWork atomic.Bool
	var wg sync.WaitGroup

	// Useful-work goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		var x uint64 = 1
		for !stopWork.Load() {
			x = cpuWork(x)
			workDone.Add(1)
		}
		runtime.KeepAlive(x)
	}()

	// Producer: push as fast as possible until stop.
	var stopRing atomic.Bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; !stopRing.Load(); i++ {
			produce(i)
		}
	}()

	// Consumer: pop one, sleep a bit (simulates slow downstream).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stopRing.Load() {
			_ = consume()
			time.Sleep(100 * time.Microsecond)
		}
	}()

	time.Sleep(duration)
	stopRing.Store(true)
	stopWork.Store(true)

	// Drain anything left so producer can exit.
	go func() {
		for {
			if stopRing.Load() {
				_ = consume()
			}
			time.Sleep(time.Millisecond)
			return
		}
	}()
	wg.Wait()
	cleanup()

	// Report ops/sec of the useful-work goroutine as the benchmark metric.
	opsPerSec := float64(workDone.Load()) / duration.Seconds()
	b.ReportMetric(opsPerSec, "work-ops/s")
	b.ReportMetric(float64(workDone.Load()), "total-work-ops")
}

// Because each benchmark runs its own duration-gated loop, b.N doesn't drive
// iterations. Use b.N=1 by setting a short bench timeout manually — testing
// framework will call once per invocation.

func BenchmarkCPUEfficiency_GoringRing(b *testing.B) {
	r := goring.NewRing[int](16)
	for i := 0; i < b.N; i++ {
		benchWorkWhileRingBusy(b,
			func(x int) { r.PushWait(x) },
			func() int { return r.PopWait() },
			func() {},
		)
	}
}

// manyProducersVsWork: 8 producers all competing for a tiny buffer (cap=8),
// 1 slow consumer (200µs per pop). Under GOMAXPROCS<8, at most a subset of
// producers can run at once. Blocked producers either park (goring, channel)
// or spin-CAS (Workiva). A parallel "useful work" goroutine measures how
// much CPU the ring leaves for other processing.
func benchManyProducersVsWork(
	b *testing.B,
	produce func(int),
	consume func() int,
	cleanup func(),
) {
	const (
		duration    = 1500 * time.Millisecond
		numProds    = 8
		consumerPau = 200 * time.Microsecond
	)
	var workDone atomic.Uint64
	var stop atomic.Bool
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		var x uint64 = 1
		for !stop.Load() {
			x = cpuWork(x)
			workDone.Add(1)
		}
		runtime.KeepAlive(x)
	}()

	for p := 0; p < numProds; p++ {
		wg.Add(1)
		go func(pid int) {
			defer wg.Done()
			for i := 0; !stop.Load(); i++ {
				produce(pid*1_000_000 + i)
			}
		}(p)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			_ = consume()
			time.Sleep(consumerPau)
		}
	}()

	time.Sleep(duration)
	stop.Store(true)
	// Drain so producers can exit.
	go func() {
		for i := 0; i < numProds*16; i++ {
			_ = consume()
		}
	}()
	wg.Wait()
	cleanup()

	opsPerSec := float64(workDone.Load()) / duration.Seconds()
	b.ReportMetric(opsPerSec, "work-ops/s")
	b.ReportMetric(float64(workDone.Load()), "total-work-ops")
}

func BenchmarkManyProducers_GoringRing(b *testing.B) {
	r := goring.NewRing[int](8)
	for i := 0; i < b.N; i++ {
		benchManyProducersVsWork(b,
			func(x int) { r.PushWait(x) },
			func() int { return r.PopWait() },
			func() {},
		)
	}
}

func BenchmarkManyProducers_Channel(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ch := make(chan int, 8)
		benchManyProducersVsWork(b,
			func(x int) { ch <- x },
			func() int { return <-ch },
			func() {},
		)
	}
}

func BenchmarkManyProducers_WorkivaRing(b *testing.B) {
	for i := 0; i < b.N; i++ {
		r := gds.NewRingBuffer(8)
		benchManyProducersVsWork(b,
			func(x int) { _ = r.Put(x) },
			func() int { v, _ := r.Get(); n, _ := v.(int); return n },
			func() { r.Dispose() },
		)
	}
}

func BenchmarkCPUEfficiency_Channel(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ch := make(chan int, 16)
		benchWorkWhileRingBusy(b,
			func(x int) { ch <- x },
			func() int { return <-ch },
			func() { close(ch) },
		)
	}
}

func BenchmarkCPUEfficiency_WorkivaRing(b *testing.B) {
	for i := 0; i < b.N; i++ {
		r := gds.NewRingBuffer(16)
		benchWorkWhileRingBusy(b,
			func(x int) { _ = r.Put(x) },
			func() int { v, _ := r.Get(); n, _ := v.(int); return n },
			func() { r.Dispose() },
		)
	}
}
