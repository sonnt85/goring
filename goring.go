package goring

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

var (
	ErrTooManyDataToWrite = errors.New("too many data to write")
	ErrIsFull             = errors.New("ringbuffer is full")
	ErrIsEmpty            = errors.New("ringbuffer is empty")
	ErrAccuqireLock       = errors.New("no lock to accquire")
)

const (
	mutexLocked = 1 << iota // mutex is locked
	mutexWoken
	mutexStarving
	mutexWaiterShift = iota
)

// Mutex is a locker which supports TryLock.
type Mutex struct {
	sync.Mutex
}

func (m *Mutex) LockSmart() {
	for {
		if m.TryLock() {
			return
		}
		runtime.Gosched()
	}
}

func (m *Mutex) Count() int {
	v := atomic.LoadInt32((*int32)(unsafe.Pointer(&m.Mutex)))
	v = v >> mutexWaiterShift
	v = v + (v & mutexLocked)
	return int(v)
}

func (m *Mutex) IsLocked() bool {
	state := atomic.LoadInt32((*int32)(unsafe.Pointer(&m.Mutex)))
	return state&mutexLocked == mutexLocked
}

func (m *Mutex) IsWoken() bool {
	state := atomic.LoadInt32((*int32)(unsafe.Pointer(&m.Mutex)))
	return state&mutexWoken == mutexWoken
}

func (m *Mutex) IsStarving() bool {
	state := atomic.LoadInt32((*int32)(unsafe.Pointer(&m.Mutex)))
	return state&mutexStarving == mutexStarving
}

// RingBuffer is a circular buffer that implement io.ReaderWriter interface.
type RingBuffer[T any] struct {
	buf    []T
	size   int
	r      int // next position to read
	w      int // next position to write
	isFull bool
	mu     Mutex
}

// New returns a new RingBuffer whose buffer has the given size.
func NewRing[T any](size int) *RingBuffer[T] {

	return &RingBuffer[T]{
		buf:  make([]T, size),
		size: size,
	}
}

func (r *RingBuffer[T]) String() string {
	return fmt.Sprintf("Cap: %d\nLength: %d\nReadAt: %d\nWrireAt: %d", r.size, r.Length(), r.r, r.w)
}

func (r *RingBuffer[T]) read(p []T) (n int, err error) {
	if r.w == r.r && !r.isFull {
		return 0, ErrIsEmpty
	}

	if r.w > r.r {
		n = r.w - r.r
		if n > len(p) {
			n = len(p)
		}
		copy(p, r.buf[r.r:r.r+n])
		r.r = (r.r + n) % r.size
		return
	}

	n = r.size - r.r + r.w
	if n > len(p) {
		n = len(p)
	}

	if r.r+n <= r.size {
		copy(p, r.buf[r.r:r.r+n])
	} else {
		c1 := r.size - r.r
		copy(p, r.buf[r.r:r.size])
		c2 := n - c1
		copy(p[c1:], r.buf[0:c2])
	}
	r.r = (r.r + n) % r.size

	r.isFull = false

	return n, err
}

func (r *RingBuffer[T]) write(p []T) (n int, err error) {
	if r.isFull {
		return 0, ErrIsFull
	}

	var avail int
	if r.w >= r.r {
		avail = r.size - r.w + r.r
	} else {
		avail = r.r - r.w
	}

	if len(p) > avail {
		err = ErrTooManyDataToWrite
		p = p[:avail]
	}
	n = len(p)

	if r.w >= r.r {
		c1 := r.size - r.w
		if c1 >= n {
			copy(r.buf[r.w:], p)
			r.w += n
		} else {
			copy(r.buf[r.w:], p[:c1])
			c2 := n - c1
			copy(r.buf[0:], p[c1:])
			r.w = c2
		}
	} else {
		copy(r.buf[r.w:], p)
		r.w += n
	}

	if r.w == r.size {
		r.w = 0
	}
	if r.w == r.r {
		r.isFull = true
	}

	return n, err
}

func (r *RingBuffer[T]) writeElement(c T) error {
	if r.w == r.r && r.isFull {
		return ErrIsFull
	}
	r.buf[r.w] = c
	r.w++

	if r.w == r.size {
		r.w = 0
	}
	if r.w == r.r {
		r.isFull = true
	}

	return nil
}

// Read reads up to len(p) bytes into p. It returns the number of bytes read (0 <= n <= len(p)) and any error encountered. Even if Read returns n < len(p), it may use all of p as scratch space during the call. If some data is available but not len(p) bytes, Read conventionally returns what is available instead of waiting for more.
// When Read encounters an error or end-of-file condition after successfully reading n > 0 bytes, it returns the number of bytes read. It may return the (non-nil) error from the same call or return the error (and n == 0) from a subsequent call.
// Callers should always process the n > 0 bytes returned before considering the error err. Doing so correctly handles I/O errors that happen after reading some bytes and also both of the allowed EOF behaviors.
func (r *RingBuffer[T]) Read(p []T) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}

	r.mu.LockSmart()
	n, err = r.read(p)
	r.mu.Unlock()
	return n, err
}

// TryRead read up to len(p) elements into p like Read but it is not blocking.
// If it has not succeeded to accquire the lock, it return 0 as n and ErrAccuqireLock.
func (r *RingBuffer[T]) TryRead(p []T) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}

	ok := r.mu.TryLock()
	if !ok {
		return 0, ErrAccuqireLock
	}

	n, err = r.read(p)
	r.mu.Unlock()
	return n, err
}

// Pop reads and returns the next element from the input or ErrIsEmpty.
func (r *RingBuffer[T]) Pop() (b T, err error) {
	r.mu.LockSmart()
	if r.w == r.r && !r.isFull {
		r.mu.Unlock()
		return *new(T), ErrIsEmpty
	}
	b = r.buf[r.r]
	r.r++
	if r.r == r.size {
		r.r = 0
	}

	r.isFull = false
	r.mu.Unlock()
	return b, err
}

// TryRead read up to len(p) elements into p like Read but it is not blocking.
// If it has not succeeded to accquire the lock, it return 0 as n and ErrAccuqireLock.
func (r *RingBuffer[T]) TryPop() (p T, err error) {
	retT := make([]T, 1)
	if _, err = r.TryRead(retT); err == nil && len(retT) == 1 {
		return retT[0], nil
	} else {
		return retT[0], err
	}
}

// Write writes len(p) bytes from p to the underlying buf.
// It returns the number of bytes written from p (0 <= n <= len(p)) and any error encountered that caused the write to stop early.
// Write returns a non-nil error if it returns n < len(p).
// Write must not modify the slice data, even temporarily.
func (r *RingBuffer[T]) Write(p []T) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.LockSmart()
	n, err = r.write(p)
	r.mu.Unlock()

	return n, err
}

// TryWrite writes len(p) bytes from p to the underlying buf like Write, but it is not blocking.
// If it has not succeeded to accquire the lock, it return 0 as n and ErrAccuqireLock.
func (r *RingBuffer[T]) TryWrite(p []T) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	ok := r.mu.TryLock()
	if !ok {
		return 0, ErrAccuqireLock
	}

	n, err = r.write(p)
	r.mu.Unlock()

	return n, err
}

// Push writes one element into buffer, and returns ErrIsFull if buffer is full.
func (r *RingBuffer[T]) Push(c T) error {
	r.mu.LockSmart()
	err := r.writeElement(c)
	r.mu.Unlock()
	return err
}

// TrywriteElementElement writes one element into buffer without blocking.
// If it has not succeeded to accquire the lock, it return ErrAccuqireLock.
func (r *RingBuffer[T]) TryPush(c T) error {
	ok := r.mu.TryLock()
	if !ok {
		return ErrAccuqireLock
	}

	err := r.writeElement(c)
	r.mu.Unlock()
	return err
}

// Length return the length of available read bytes.
func (r *RingBuffer[T]) Length() int {
	r.mu.LockSmart()
	defer r.mu.Unlock()

	if r.w == r.r {
		if r.isFull {
			return r.size
		}
		return 0
	}

	if r.w > r.r {
		return r.w - r.r
	}

	return r.size - r.r + r.w
}

// Capacity returns the size of the underlying buffer.
func (r *RingBuffer[T]) Capacity() int {
	return r.size
}

// Free returns the length of available bytes to write.
func (r *RingBuffer[T]) Free() int {
	r.mu.LockSmart()
	defer r.mu.Unlock()

	if r.w == r.r {
		if r.isFull {
			return 0
		}
		return r.size
	}

	if r.w < r.r {
		return r.r - r.w
	}

	return r.size - r.w + r.r
}

// Copy returns all available read elements. It does not move the read pointer and only copy the available data.
func (r *RingBuffer[T]) Copy() []T {
	r.mu.LockSmart()
	defer r.mu.Unlock()

	if r.w == r.r {
		if r.isFull {
			buf := make([]T, r.size)
			copy(buf, r.buf[r.r:])
			copy(buf[r.size-r.r:], r.buf[:r.w])
			return buf
		}
		return nil
	}

	if r.w > r.r {
		buf := make([]T, r.w-r.r)
		copy(buf, r.buf[r.r:r.w])
		return buf
	}

	n := r.size - r.r + r.w
	buf := make([]T, n)

	if r.r+n < r.size {
		copy(buf, r.buf[r.r:r.r+n])
	} else {
		c1 := r.size - r.r
		copy(buf, r.buf[r.r:r.size])
		c2 := n - c1
		copy(buf[c1:], r.buf[0:c2])
	}

	return buf
}

// IsFull returns this ringbuffer is full.
func (r *RingBuffer[T]) IsFull() bool {
	r.mu.LockSmart()
	defer r.mu.Unlock()
	return r.isFull
}

// IsEmpty returns this ringbuffer is empty.
func (r *RingBuffer[T]) IsEmpty() bool {
	r.mu.LockSmart()
	defer r.mu.Unlock()
	return !r.isFull && r.w == r.r
}

// Reset the read pointer and writer pointer to zero.
func (r *RingBuffer[T]) Reset() {
	r.mu.LockSmart()
	defer r.mu.Unlock()

	r.r = 0
	r.w = 0
	r.isFull = false
}
