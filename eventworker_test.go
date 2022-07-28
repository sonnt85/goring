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

func BenchmarkEventWorker(b *testing.B) {
	step := 1024 * 10
	for i := step; i < 1024*100; i += step {
		ew := NewEventWorker[string](0, i, 0, 0)
		ew.EnableWorker()
		wg := new(sync.WaitGroup)
		b.Run(fmt.Sprintf("buffer_size: %d", i), func(b *testing.B) {
			wg.Add(1)
			go func() {
				for j := 0; j < b.N; j++ {
					ew.Submit("fmt", 0, nil, printInit, j)
				}
				wg.Done()
			}()
			wg.Wait()
			ew.WaitUntilAllTaskFinish()
		})
	}
}

func printInit(i interface{}, kkk ...int32) int {
	// i = i + 1
	fmt.Printf("i/kkk - %+v/%+v\n", i, kkk)

	ret, _ := i.(int)
	return ret
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
				if val, ok := paras[0].Interface().(int); ok {
					log.Println("timeout, deleting client: ", val)
				}
			}
			// log.Info("timeout, deleting client:")
		}
	})
	ew.EnableWorker() //start run worker

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		var i int
		for i = 0; i < 8; i++ {
			//, any([]int{1, 2, 3}
			_, err := ew.Submit("fmt", 0, nil, printInit, i, int32(i))
			require.Nil(t, err)
			runtime.Gosched() //for other goroutine run
		}
		wg.Done() //sumit done
	}()
	wg.Wait() //wait finish submit
	ew.WaitUntilAllTaskFinish()
	time.Sleep(time.Second * 2)
	ppjson.Println(ew.Stats())
	fmt.Println("Done")
}

func TestGoSched(t *testing.T) {
	done := false

	go func() {
		done = true
	}()

	for !done {
		// runtime.Gosched()
	}
	fmt.Println("done!")
}
