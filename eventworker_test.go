package goring

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lunny/log"
	"github.com/sonnt85/gosutils/funcmap"
	"github.com/sonnt85/gosutils/ppjson"
	"github.com/stretchr/testify/require"
	"github.com/zeebo/assert"
)

// benchNoopWork is a silent work function used in benchmarks so that the
// benchmarked throughput is not dominated by fmt.Printf I/O.
func benchNoopWork(i interface{}, _ ...int32) int {
	ret, _ := i.(int)
	return ret
}

// printInit is kept for TestEventWorker which uses printed output as a
// visual smoke test of task execution.
func printInit(i interface{}, kkk ...int32) int {
	fmt.Printf("i/kkk - %+v/%+v\n", i, kkk)
	ret, _ := i.(int)
	return ret
}

func BenchmarkEventWorker(b *testing.B) {
	for _, size := range []int{1024, 4096, 16384, 65536} {
		size := size
		b.Run(fmt.Sprintf("buf=%d", size), func(b *testing.B) {
			ew := NewEventWorker[string](0, size, 0, 0)
			ew.EnableWorker()
			b.ResetTimer()
			for j := 0; j < b.N; j++ {
				_, _ = ew.Submit("noop", 0, nil, benchNoopWork, j)
			}
			ew.WaitUntilAllTaskFinish()
		})
	}
}

func TestTimeAfter(t *testing.T) {
	timeoutCh := time.After(time.Millisecond * 100)

	<-timeoutCh
	t.Log("timeoutCh <-")

	select {
	case <-timeoutCh:
		t.Log("timeoutCh <- again")
	default:
		// Expected: time.After channel has no buffered value after first receive.
		t.Log("timeoutCh already drained, as expected")
	}
}
func TestRingBuffer_PushWaitTimeOut(t *testing.T) {
	rb := NewRing[int](10)

	err := rb.PushWaitTimeOut(1, time.Second*100)
	assert.NoError(t, err)
	var n int
	n, err = rb.WriteWaitTimeOut([]int{2, 3, 4, 5, 6, 7, 8, 9}, time.Second*100)
	t.Log("n: ", n)
	assert.NoError(t, err)

	err = rb.PushWaitTimeOut(3, time.Second*100)
	assert.NoError(t, err)
	p := make([]int, 100)
	_, err = rb.ReadWaitTimeOut(p, time.Second*100)
	assert.NoError(t, err)
	t.Logf("%+v", p)
}

func TestEventWorker(t *testing.T) {
	ew := NewEventWorker[string](0, 4, time.Second*2, time.Second*1)
	ew.OnEvictedSavedTask(func(k string, t *funcmap.Task[string]) {
		if t.Name == "fmt" {
			_, paras, _, err := t.GetFuncDetail()
			if err != nil {
				log.Info("Can not run: ", err)
			}
			if len(paras) <= 2 {
				if val, ok := paras[0].(int); ok {
					log.Println("timeout, deleting client: ", val)
				}
			}
		}
	})
	ew.EnableWorker()

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 8; i++ {
			_, err := ew.Submit("fmt", 0, nil, printInit, i, int32(i))
			require.Nil(t, err)
			runtime.Gosched()
		}
	}()
	wg.Wait()
	ew.WaitUntilAllTaskFinish()
	time.Sleep(time.Second * 2)
	ppjson.Println(ew.Stats())
}

func TestGoSched(t *testing.T) {
	var done int32

	go func() {
		atomic.StoreInt32(&done, 1)
	}()

	for atomic.LoadInt32(&done) == 0 {
		runtime.Gosched()
	}
}
