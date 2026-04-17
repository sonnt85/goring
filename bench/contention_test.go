// Contention benchmarks — these exercise the "blocking under load" scenario
// where goring's sync.Cond-based Wait/Signal should shine: goroutines park
// when buffer fills/empties instead of spinning on CAS.
//
// The small-capacity buffer (cap=16) vs many producers (P=8) forces writers
// to block repeatedly. Each Workiva Put on a full buffer spins; each goring
// PushWait on a full buffer parks the goroutine.
//
// Measure: total throughput AND CPU efficiency (GOMAXPROCS-bounded).
package bench

import (
	"runtime"
	"sync"
	"testing"
	"time"

	gds "github.com/Workiva/go-datastructures/queue"
	"github.com/sonnt85/goring"
)

const (
	contendedCap   = 16   // tiny buffer — forces blocking
	contendedItems = 1000 // total items each benchmark iteration moves through
)

// --- MPMC: 4 producers / 4 consumers, buffer cap=16 ---
// Producers outnumber buffer slots — writers WILL block frequently.

func BenchmarkMPMC_GoringRing(b *testing.B) {
	for i := 0; i < b.N; i++ {
		r := goring.NewRing[int](contendedCap)
		var wg sync.WaitGroup
		const P, C = 4, 4
		wg.Add(P + C)
		for p := 0; p < P; p++ {
			go func() {
				defer wg.Done()
				for j := 0; j < contendedItems/P; j++ {
					r.PushWait(j)
				}
			}()
		}
		for c := 0; c < C; c++ {
			go func() {
				defer wg.Done()
				for j := 0; j < contendedItems/C; j++ {
					_ = r.PopWait()
				}
			}()
		}
		wg.Wait()
	}
}

func BenchmarkMPMC_Channel(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ch := make(chan int, contendedCap)
		var wg sync.WaitGroup
		const P, C = 4, 4
		wg.Add(P + C)
		for p := 0; p < P; p++ {
			go func() {
				defer wg.Done()
				for j := 0; j < contendedItems/P; j++ {
					ch <- j
				}
			}()
		}
		for c := 0; c < C; c++ {
			go func() {
				defer wg.Done()
				for j := 0; j < contendedItems/C; j++ {
					<-ch
				}
			}()
		}
		wg.Wait()
	}
}

func BenchmarkMPMC_WorkivaRing(b *testing.B) {
	for i := 0; i < b.N; i++ {
		r := gds.NewRingBuffer(contendedCap)
		var wg sync.WaitGroup
		const P, C = 4, 4
		wg.Add(P + C)
		for p := 0; p < P; p++ {
			go func() {
				defer wg.Done()
				for j := 0; j < contendedItems/P; j++ {
					_ = r.Put(j)
				}
			}()
		}
		for c := 0; c < C; c++ {
			go func() {
				defer wg.Done()
				for j := 0; j < contendedItems/C; j++ {
					_, _ = r.Get()
				}
			}()
		}
		wg.Wait()
	}
}

// --- Slow consumer: producer must genuinely wait each iteration ---
// 1 fast producer + 1 slow consumer (50µs between pops). Buffer cap=4.
// Producer fills buffer, then BLOCKS on every Push until consumer drains one.
// This is the scenario where goring's Wait (park) should outperform
// lock-free spin in CPU efficiency, and where a channel's built-in parking
// has the same property.

func BenchmarkSlowConsumer_GoringRing(b *testing.B) {
	r := goring.NewRing[int](4)
	done := make(chan struct{})
	go func() {
		for i := 0; i < b.N; i++ {
			_ = r.PopWait()
			// Busy work that yields — simulates slow downstream.
			runtime.Gosched()
			time.Sleep(50 * time.Microsecond)
		}
		close(done)
	}()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.PushWait(i)
	}
	<-done
}

func BenchmarkSlowConsumer_Channel(b *testing.B) {
	ch := make(chan int, 4)
	done := make(chan struct{})
	go func() {
		for i := 0; i < b.N; i++ {
			<-ch
			runtime.Gosched()
			time.Sleep(50 * time.Microsecond)
		}
		close(done)
	}()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch <- i
	}
	<-done
}

func BenchmarkSlowConsumer_WorkivaRing(b *testing.B) {
	r := gds.NewRingBuffer(4)
	done := make(chan struct{})
	go func() {
		for i := 0; i < b.N; i++ {
			_, _ = r.Get()
			runtime.Gosched()
			time.Sleep(50 * time.Microsecond)
		}
		close(done)
	}()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Put(i)
	}
	<-done
}
