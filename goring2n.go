package goring

import (
	"fmt"
	"runtime"
	"sync/atomic"
)

type Ring2N[T any] struct {
	length        uint32
	bitWiseLength uint32
	writePointer  uint32 // next position to write
	buffer        []T
	readerPointer uint32
	isFull        bool
	mu            Mutex
}

func NewRing2N[T any](size uint32) (Ring2N[T], error) {

	if size&(size-1) != 0 {
		return Ring2N[T]{}, InvalidBufferSize
	}

	return Ring2N[T]{
		buffer:        make([]T, size+1, size+1),
		length:        size,
		bitWiseLength: size - 1,
		writePointer:  0,
		readerPointer: ^(uint32(0)),
	}, nil
}

func (r *Ring2N[T]) Pop() T {
	var newIndex = atomic.AddUint32(&r.readerPointer, 1)
	for newIndex >= atomic.LoadUint32(&r.writePointer) {
		runtime.Gosched()
	}
	r.mu.LockSmart()
	defer r.mu.Unlock()
	return r.buffer[newIndex&r.bitWiseLength]
}

// Capacity returns the size of the underlying buffer.
func (r *Ring2N[T]) Capacity() uint32 {
	return r.length
}

// Capacity returns the size of the underlying buffer.
func (r *Ring2N[T]) TrywriteElementElement(e T) error {
	return nil
}

// Free returns the length of available elements to write.
func (r *Ring2N[T]) Free() uint32 {
	var newIndex = atomic.AddUint32(&r.readerPointer, 1)
	return atomic.LoadUint32(&r.writePointer) - newIndex
}

// Length return the length of available read elements.
func (r *Ring2N[T]) Length() uint32 {
	lastTailReaderPointerPosition := atomic.LoadUint32(&r.writePointer) + r.length
	currentReadPosition := atomic.LoadUint32(&r.readerPointer) + r.length
	if currentReadPosition < lastTailReaderPointerPosition {
		lastTailReaderPointerPosition = currentReadPosition
	}

	return atomic.LoadUint32(&r.writePointer) - lastTailReaderPointerPosition
}

func (r *Ring2N[T]) IsEmpty() bool {
	var newIndex = atomic.AddUint32(&r.readerPointer, 1)
	return newIndex >= atomic.LoadUint32(&r.writePointer)
}

func (r *Ring2N[T]) IsFull() bool {
	lastTailReaderPointerPosition := atomic.LoadUint32(&r.writePointer) + r.length
	currentReadPosition := atomic.LoadUint32(&r.readerPointer) + r.length
	if currentReadPosition < lastTailReaderPointerPosition {
		lastTailReaderPointerPosition = currentReadPosition
	}

	if lastTailReaderPointerPosition > r.writePointer {
		return false
	}
	return true
}

func (r *Ring2N[T]) Push(value T) {

	var lastTailReaderPointerPosition uint32
	var currentReadPosition uint32
	for {
		lastTailReaderPointerPosition = atomic.LoadUint32(&r.writePointer) + r.length
		currentReadPosition = atomic.LoadUint32(&r.readerPointer) + r.length
		if currentReadPosition < lastTailReaderPointerPosition {
			lastTailReaderPointerPosition = currentReadPosition
		}

		if lastTailReaderPointerPosition > r.writePointer {
			r.mu.LockSmart()
			r.buffer[r.writePointer&r.bitWiseLength] = value
			atomic.AddUint32(&r.writePointer, 1)
			r.mu.Unlock()
			return
		}
		runtime.Gosched()
	}
}

func (r *Ring2N[T]) Write(p []T) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.LockSmart()
	n, err := r.write(p)
	r.mu.Unlock()

	return int(n), err
}

func (r *Ring2N[T]) write(p []T) (n int, err error) {
	if r.isFull {
		return 0, ErrIsFull
	}

	var avail uint32
	if r.writePointer >= r.readerPointer {
		avail = r.length - r.writePointer + r.readerPointer
	} else {
		avail = r.readerPointer - r.writePointer
	}

	if uint32(len(p)) > avail {
		err = ErrTooManyDataToWrite
		p = p[:avail]
	}
	n = len(p)

	if r.writePointer >= r.readerPointer {
		c1 := r.length - r.writePointer
		if c1 >= uint32(n) {
			copy(r.buffer[r.writePointer:], p)
			r.writePointer += uint32(n)
		} else {
			copy(r.buffer[r.writePointer:], p[:c1])
			c2 := uint32(n) - c1
			copy(r.buffer[0:], p[c1:])
			r.writePointer = c2
		}
	} else {
		copy(r.buffer[r.writePointer:], p)
		r.writePointer += uint32(n)
	}

	if r.writePointer == r.length {
		r.writePointer = 0
	}
	if r.writePointer == r.readerPointer {
		r.isFull = true
	}
	return n, err
}

func (r *Ring2N[T]) read(p []T) (int, error) {
	var n int
	var err error
	if r.writePointer == r.readerPointer && !r.isFull {
		return 0, ErrIsEmpty
	}

	if r.writePointer > r.readerPointer {
		n = int(r.writePointer - r.readerPointer)
		if n > len(p) {
			n = len(p)
		}
		copy(p, r.buffer[r.readerPointer:r.readerPointer+uint32(n)])
		r.readerPointer = (r.readerPointer + uint32(n)) % r.length
		return n, err
	}

	n = int(r.length - r.readerPointer + r.writePointer)
	if n > len(p) {
		n = len(p)
	}

	if r.readerPointer+uint32(n) <= r.length {
		copy(p, r.buffer[r.readerPointer:r.readerPointer+uint32(n)])
	} else {
		c1 := r.length - r.readerPointer
		copy(p, r.buffer[r.readerPointer:r.length])
		c2 := uint32(n) - c1
		copy(p[c1:], r.buffer[0:c2])
	}
	r.readerPointer = (r.readerPointer + uint32(n)) % r.length

	r.isFull = false

	return n, err
}

func (r *Ring2N[T]) String() string {
	return fmt.Sprintf("Cap: %d\nLength: %d\nReadAt: %d\nWrireAt: %d", r.length+1, r.Length(), r.readerPointer, r.writePointer)
}
