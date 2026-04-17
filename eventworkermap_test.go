package goring

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/lunny/log"
	"github.com/sonnt85/gosutils/funcmap"
	"github.com/sonnt85/gosutils/ppjson"
	"github.com/stretchr/testify/require"
)

func BenchmarkEventWorkerMap(b *testing.B) {
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

// _printInit mirrors printInit but writes to the test log instead of stdout.
// Kept as a regular function (not a method on *testing.T) to match the
// funcmap.Task signature expected by EventWorkerMap.Submit.
func _printInit(i interface{}, kkk ...int32) int {
	fmt.Println("i: ", i, " kkk: ", kkk)
	ret, _ := i.(int)
	return ret
}

func TestEventWorkerMap(t *testing.T) {
	ew := NewEventWorkerMap[string](0, 4, time.Second*2, time.Second*1)
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

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 8; i++ {
			_, err := ew.Submit("fmt", 0, func() string { return "_printInit" }, _printInit, i, int32(i))
			require.Nil(t, err)
			runtime.Gosched()
		}
	}()
	wg.Wait()
	ew.EnableWorker()
	ew.WaitUntilAllTaskFinish()
	time.Sleep(time.Second * 3)
	ppjson.Println(ew.Stats())
}
