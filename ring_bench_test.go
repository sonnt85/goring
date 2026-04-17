package goring

import (
	"sync"
	"testing"
)

const benchRingSize = 1024

func BenchmarkRing_PushPop_Serial(b *testing.B) {
	r := NewRing[int](benchRingSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.PushWait(i)
		_ = r.PopWait()
	}
}

func BenchmarkRing_PushPop_SPSC(b *testing.B) {
	r := NewRing[int](benchRingSize)
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

func BenchmarkRing_Write_Read_Batch(b *testing.B) {
	r := NewRing[int](benchRingSize * 4)
	batch := make([]int, 64)
	for i := range batch {
		batch[i] = i
	}
	out := make([]int, 64)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = r.Write(batch)
		_, _ = r.Read(out)
	}
}
