package goring

import (
	"errors"
	"sync"
	"sync/atomic"
)

var (
	MaxConsumerError  = errors.New("max amount of consumers reached cannot create any more")
	InvalidBufferSize = errors.New("buffer must be of size 2^n")
)

type RingMutipleReader[T any] struct {
	length            uint32
	bitWiseLength     uint32
	headPointer       uint32 // next position to write
	maximumConsumerId uint32
	maxConsumers      int
	*sync.RWMutex
	*sync.Cond
	readEvent         *sync.Cond
	buffer            []T
	readerPointers    []uint32
	readerActiveFlags []uint32
}

type Consumer[T any] struct {
	ring *RingMutipleReader[T]
	id   uint32
}

func NewRingMutipleReader[T any](size uint32, maxConsumers uint32) (rmr *RingMutipleReader[T], err error) {

	if size&(size-1) != 0 {
		err = InvalidBufferSize
		return
	}

	rmr = &RingMutipleReader[T]{
		buffer:            make([]T, size+1, size+1),
		length:            size,
		bitWiseLength:     size - 1,
		headPointer:       0,
		maximumConsumerId: 0,
		maxConsumers:      int(maxConsumers),
		RWMutex:           &sync.RWMutex{},
		readerPointers:    make([]uint32, maxConsumers),
		readerActiveFlags: make([]uint32, maxConsumers),
	}
	rmr.readEvent = sync.NewCond(&sync.Mutex{})
	rmr.Cond = sync.NewCond(rmr.RWMutex)
	return
}

/*
NewConsumer
Create a consumer by assigning it the id of the first empty position in the consumerPosition array. A nil value represents
an unclaimed/not used consumer.
Locks can be used as it has no effect on read/write operations and is only to keep consumer consistency, thus the
algorithm is still lockless For best performance, consumers should be preallocated before starting buffer operations
*/
func (r *RingMutipleReader[T]) NewConsumer() (Consumer[T], error) {
	r.RLock()
	defer r.RUnlock()

	var newConsumerId = r.maxConsumers

	for i, _ := range r.readerActiveFlags {
		if atomic.LoadUint32(&r.readerActiveFlags[i]) == 0 {
			newConsumerId = i
			break
		}
	}

	if newConsumerId == r.maxConsumers {
		return Consumer[T]{}, MaxConsumerError
	}

	if uint32(newConsumerId) >= r.maximumConsumerId {
		atomic.AddUint32(&r.maximumConsumerId, 1)
	}

	r.readerPointers[newConsumerId] = atomic.LoadUint32(&r.headPointer) - 1
	atomic.StoreUint32(&r.readerActiveFlags[newConsumerId], 1)

	return Consumer[T]{
		id:   uint32(newConsumerId),
		ring: r,
	}, nil
}

func (r *RingMutipleReader[T]) removeConsumer(consumerId uint32) {
	r.RLock()
	defer r.RUnlock()

	atomic.StoreUint32(&r.readerActiveFlags[consumerId], 0)
	atomic.CompareAndSwapUint32(&r.maximumConsumerId, consumerId, r.maximumConsumerId-1)
}

func (consumer *Consumer[T]) Remove() {
	consumer.ring.removeConsumer(consumer.id)
}

func (consumer *Consumer[T]) Get() T {
	return consumer.ring.readIndex(consumer.id)
}

func (r *RingMutipleReader[T]) Write(value T) {

	var lastTailReaderPointerPosition uint32
	var currentReadPosition uint32
	var i uint32
	/*
		We are blocking until the all at least one space is available in the buffer to write.
		As overflow properties of uint32 are utilized to ensure slice index boundaries are adhered too we add the length
		of buffer to current consumer read positions allowing us to determine the least read consumer.
		For example: buffer of size 2
		uint8 head = 1
		uint8 tail = 255
		tail + 2 => 1 with overflow, same as buffer
	*/
	for {
		lastTailReaderPointerPosition = atomic.LoadUint32(&r.headPointer) + r.length

		for i = 0; i < atomic.LoadUint32(&r.maximumConsumerId); i++ {

			if atomic.LoadUint32(&r.readerActiveFlags[i]) == 1 {
				currentReadPosition = atomic.LoadUint32(&r.readerPointers[i]) + r.length

				if currentReadPosition < lastTailReaderPointerPosition {
					lastTailReaderPointerPosition = currentReadPosition
				}
			}
		}

		if lastTailReaderPointerPosition > r.headPointer {
			r.buffer[r.headPointer&r.bitWiseLength] = value
			atomic.AddUint32(&r.headPointer, 1)
			r.Broadcast()
			return
		} else {
			r.readEvent.L.Lock()
			r.readEvent.Wait()
			r.readEvent.L.Unlock()
		}
		// runtime.Gosched()
	}
}

func (r *RingMutipleReader[T]) readIndex(consumerId uint32) T {

	var newIndex = atomic.AddUint32(&r.readerPointers[consumerId], 1)

	// yield until work is available
	for newIndex >= atomic.LoadUint32(&r.headPointer) {
		r.Lock()
		r.Wait()
		r.Unlock()
		// runtime.Gosched()
	}
	r.readEvent.Broadcast()
	return r.buffer[newIndex&r.bitWiseLength]
}
