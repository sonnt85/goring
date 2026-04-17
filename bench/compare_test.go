// Package bench compares goring against ecosystem alternatives and the
// standard buffered channel.
//
// Covered:
//   - sonnt85/goring.Ring[T] (blocking Push/Pop, Write/Read batch)
//   - sonnt85/goring.RingBytes (io.Reader/Writer ring)
//   - smallnest/ringbuffer ([]byte ring with blocking)
//   - Workiva/go-datastructures/queue.RingBuffer (lock-free MPMC)
//   - chan T (stdlib buffered channel, same capacity)
//
// Run: go test -bench=. -benchmem -run=^$ -benchtime=2s
package bench

import (
	"sync"
	"testing"

	gds "github.com/Workiva/go-datastructures/queue"
	sring "github.com/smallnest/ringbuffer"
	"github.com/sonnt85/goring"
)

const benchRingSize = 1024

// --- serial push+pop in a single goroutine ---

func BenchmarkSerial_GoringRing(b *testing.B) {
	r := goring.NewRing[int](benchRingSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.PushWait(i)
		_ = r.PopWait()
	}
}

func BenchmarkSerial_Channel(b *testing.B) {
	ch := make(chan int, benchRingSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch <- i
		<-ch
	}
}

func BenchmarkSerial_WorkivaRing(b *testing.B) {
	r := gds.NewRingBuffer(benchRingSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Put(i)
		_, _ = r.Get()
	}
}

// --- single-producer single-consumer across two goroutines ---

func BenchmarkSPSC_GoringRing(b *testing.B) {
	r := goring.NewRing[int](benchRingSize)
	b.ResetTimer()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < b.N; i++ {
			_ = r.PopWait()
		}
	}()
	for i := 0; i < b.N; i++ {
		r.PushWait(i)
	}
	wg.Wait()
}

func BenchmarkSPSC_Channel(b *testing.B) {
	ch := make(chan int, benchRingSize)
	b.ResetTimer()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < b.N; i++ {
			<-ch
		}
	}()
	for i := 0; i < b.N; i++ {
		ch <- i
	}
	wg.Wait()
}

func BenchmarkSPSC_WorkivaRing(b *testing.B) {
	r := gds.NewRingBuffer(benchRingSize)
	b.ResetTimer()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < b.N; i++ {
			_, _ = r.Get()
		}
	}()
	for i := 0; i < b.N; i++ {
		_ = r.Put(i)
	}
	wg.Wait()
}

// --- byte ring (io.Reader/Writer) serial comparison ---

func BenchmarkByteRing_Serial_Goring(b *testing.B) {
	r := goring.NewRingBytes(benchRingSize)
	payload := []byte("hello world")
	out := make([]byte, 32)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = r.Write(payload)
		_, _ = r.Read(out)
	}
}

func BenchmarkByteRing_Serial_Smallnest(b *testing.B) {
	r := sring.New(benchRingSize)
	payload := []byte("hello world")
	out := make([]byte, 32)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = r.Write(payload)
		_, _ = r.Read(out)
	}
}
