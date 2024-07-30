package goring

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
)

var (
	ErrTooManyDataToWrite = errors.New("too many data to write")
	ErrIsFull             = errors.New("ringbuffer is full")
	ErrIsEmpty            = errors.New("ringbuffer is empty")
	ErrAccuqireLock       = errors.New("no lock to accquire")
	ErrOverflow           = errors.New("overflow")
	ErrInvalidWriteCount  = errors.New("invalid Write count")
)

// RingBuffer is a circular buffer that implement io.ReaderWriter interface.
type RingBuffer[T any] struct {
	buf                []T
	size               int
	maxsize            int
	r                  int // next position to read
	w                  int // next position to write
	isFull             bool
	eventwrite         *sync.Cond
	eventbroacastwrite *sync.Cond
	eventread          *sync.Cond
	eventbroacastread  *sync.Cond
}

// New returns a new RingBuffer whose buffer has the given size.
func NewRing[T any](size int) *RingBuffer[T] {

	ringbuf := RingBuffer[T]{
		buf:                make([]T, size),
		size:               size,
		maxsize:            size * 16,
		eventwrite:         sync.NewCond(new(sync.RWMutex)),
		eventread:          sync.NewCond(new(sync.Mutex)),
		eventbroacastread:  sync.NewCond(new(sync.Mutex)),
		eventbroacastwrite: sync.NewCond(new(sync.Mutex)),
	}
	return &ringbuf
}

func (r *RingBuffer[T]) String() string {
	return fmt.Sprintf("Cap: %d\nLength: %d\nReadAt: %d\nWrireAt: %d", r.size, r.Length(), r.r, r.w)
}

func (r *RingBuffer[T]) Stats() (retmap map[string]interface{}) {
	retmap = make(map[string]interface{})
	retmap["Capacity"] = r.size
	retmap["Length"] = r.Length()
	retmap["Read Pointer"] = r.r
	retmap["Write Pointer"] = r.w
	return
}

//go:inline
func (r *RingBuffer[T]) read(p []T) (n int, err error) {
	if r.isempty() {
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

//go:inline
func (r *RingBuffer[T]) write(p []T) (n int, err error) {
	if r.isfull() {
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

//go:inline
func (r *RingBuffer[T]) writeElement(c T) error {
	if r.isfull() {
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

//go:inline
func (r *RingBuffer[T]) sendeventread(n int) {
	if n <= 0 {
		return
	}
	for i := 0; i < n; i++ {
		r.eventread.Signal() //for one wait
	} //for one wait
	r.eventbroacastread.Broadcast() //for everything need signal
}

//go:inline
func (r *RingBuffer[T]) sendeventwrite(n int) {
	if n <= 0 {
		return
	}
	for i := 0; i < n; i++ {
		r.eventwrite.Signal()
	} //for one wait
	r.eventbroacastwrite.Broadcast() //for everything need signal
}

// Read reads up to len(p) bytes into p. It returns the number of bytes read (0 <= n <= len(p)) and any error encountered. Even if Read returns n < len(p), it may use all of p as scratch space during the call. If some data is available but not len(p) bytes, Read conventionally returns what is available instead of waiting for more.
// When Read encounters an error or end-of-file condition after successfully reading n > 0 bytes, it returns the number of bytes read. It may return the (non-nil) error from the same call or return the error (and n == 0) from a subsequent call.
// Callers should always process the n > 0 bytes returned before considering the error err. Doing so correctly handles I/O errors that happen after reading some bytes and also both of the allowed EOF behaviors.
func (r *RingBuffer[T]) Read(p []T) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.eventwrite.L.Lock()
	n, err = r.read(p)
	r.eventwrite.L.Unlock()
	r.sendeventread(n)
	return n, err
}

// wait for the buffer to have enough wool (p) element. Then start reading all the data (len(p) elements)
func (r *RingBuffer[T]) ReadWait(p []T) (n int) {
	totalbytes := len(p)
	if totalbytes == 0 {
		return
	}
	var cntread, cnt int
	cntread = 0
	for cntread != totalbytes {
		r.eventread.L.Lock()
		if r.isempty() {
			r.eventread.L.Unlock()
			r.eventbroacastwrite.L.Lock()
			r.eventbroacastwrite.Wait()
			r.eventbroacastwrite.L.Unlock()
			r.eventread.L.Lock()
		}
		if cnt, _ = r.read(p[cntread:]); cnt != 0 {
			cntread += cnt
			r.sendeventread(cnt)
		}
		r.eventread.L.Unlock()
	}
	return totalbytes
}

func (r *RingBuffer[T]) ReadWaitTimeOut(c []T, timeout time.Duration, ctxs ...context.Context) (n int, err error) {
	if timeout <= 0 {
		timeout = 1<<63 - 1
	}
	timeoutCh := time.After(timeout)
	readSuccess := make(chan bool, 1)
	isTimeOut := false
	go func() {
		r.eventread.L.Lock()
		defer func() {
			r.eventread.L.Unlock()
		}()
		for r.isempty() {
			r.eventbroacastread.L.Lock()
			r.eventbroacastread.Wait() // Wait until the buffer is not empty or a signal is received
			r.eventbroacastread.L.Unlock()
			if isTimeOut {
				return
			}
		}
		// After being signalled, check if we've timed out before writing to the buffer
		if isTimeOut {
			return
		}
		n, err = r.read(c)
		r.sendeventread(n)
		readSuccess <- true
	}()
	ctx := context.Background()
	if len(ctxs) > 0 {
		ctx = ctxs[0]
	}

	select {
	case <-readSuccess:
		// The data was successfully pushed to the buffer
		return
	case <-ctx.Done(): // If the context is done before the goroutine is done
		isTimeOut = true
		r.eventbroacastread.Broadcast() // Wake up all goroutines waiting on r.eventbroacastread
		err = ctx.Err()
		return
	case <-timeoutCh: // If the timeout occurs before the goroutine is done
		isTimeOut = true
		r.eventbroacastread.Broadcast() // Wake up all goroutines waiting on r.eventbroacastread
		err = os.ErrDeadlineExceeded    // os.ErrDeadlineExceeded
		return
	}
}

func (r *RingBuffer[T]) ReadAll() (retval []T, err error) {
	var n int
	r.eventwrite.L.Lock()
	defer func() {
		r.eventwrite.L.Unlock()
		r.sendeventread(n)
	}()
	bufsize := r.length()
	if bufsize == 0 {
		err = ErrIsEmpty
		return
	}
	retval = make([]T, bufsize)
	n, err = r.read(retval)
	return
}

// wait not empty then read all
func (r *RingBuffer[T]) ReadAllWait() (p []T) {
	r.eventwrite.L.Lock()
	for r.isempty() {
		r.eventwrite.Wait()
	}
	bufsize := r.length()
	p = make([]T, bufsize)
	var n int
	n, _ = r.read(p) //sure read all
	r.eventwrite.L.Unlock()
	r.sendeventread(n)
	return p
}

// TryRead read up to len(p) elements into p like Read but it is not blocking.
// If it has not succeeded to accquire the lock, it return 0 as n and ErrAccuqireLock.
func (r *RingBuffer[T]) TryRead(p []T) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	ok := r.eventwrite.L.(*sync.Mutex).TryLock()
	if !ok {
		return 0, ErrAccuqireLock
	}

	n, err = r.read(p)
	r.eventwrite.L.Unlock()
	if err == nil {
		r.sendeventread(n)
	}
	return n, err
}

func (r *RingBuffer[T]) TryReadAll() ([]T, error) {
	bufsize := r.Length()
	if bufsize == 0 {
		return nil, ErrIsEmpty
	}
	p := make([]T, bufsize)
	_, err := r.TryRead(p)
	return p, err
}

// Pop reads and returns the next element from the input or ErrIsEmpty.
func (r *RingBuffer[T]) Pop() (b T, err error) {
	r.eventwrite.L.Lock()
	b, err = r.readElement()
	r.eventwrite.L.Unlock()
	if err == nil {
		r.sendeventread(1)
	}
	return b, err
}

func (r *RingBuffer[T]) readElement() (b T, err error) {
	if r.isempty() {
		return *new(T), ErrIsEmpty
	}
	b = r.buf[r.r]
	r.r++
	if r.r == r.size {
		r.r = 0
	}

	r.isFull = false
	return b, err
}

func (r *RingBuffer[T]) PopWait() (b T) {
	r.eventwrite.L.Lock()
	for r.isempty() {
		r.eventwrite.Wait()
	}
	b, _ = r.readElement() //sure is no err
	r.eventwrite.L.Unlock()
	r.sendeventread(1)
	return b
}

func (r *RingBuffer[T]) WaitUntilNotFull() {
	r.eventwrite.L.(*sync.RWMutex).RLock()
	if r.isfull() {
		r.eventwrite.L.(*sync.RWMutex).RUnlock() //unlock free lockwriteread
		r.eventbroacastread.L.Lock()
		r.eventbroacastread.Wait() //wait for any data to be read
		r.eventbroacastread.L.Unlock()
		return
	} else {
		r.eventwrite.L.(*sync.RWMutex).RUnlock()
	}
}

func (r *RingBuffer[T]) WaitUntilEmpty() {
	for {
		r.eventwrite.L.(*sync.RWMutex).RLock()
		if r.isempty() {
			r.eventwrite.L.(*sync.RWMutex).RUnlock() //unlock free lockwriteread
			return
		}
		r.eventwrite.L.(*sync.RWMutex).RUnlock() //unlock free lockwriteread
		r.eventbroacastread.L.Lock()
		r.eventbroacastread.Wait()
		r.eventbroacastread.L.Unlock()
	}
}

// TryRead read up to len(p) elements into p like Read but it is not blocking.
// If it has not succeeded to accquire the lock, it return 0 as n and ErrAccuqireLock.
func (r *RingBuffer[T]) TryPop() (p T, err error) {
	var zero T
	ok := r.eventwrite.L.(*sync.Mutex).TryLock()
	if !ok {
		return zero, ErrAccuqireLock
	}
	p, err = r.readElement()
	r.eventwrite.L.Unlock()
	if err == nil {
		r.sendeventread(1)
	}
	return
}

// Write writes len(p) bytes from p to the underlying buf.
// It returns the number of bytes written from p (0 <= n <= len(p)) and any error encountered that caused the write to stop early.
// Write returns a non-nil error if it returns n < len(p).
// Write must not modify the slice data, even temporarily.
func (r *RingBuffer[T]) Write(p []T) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.eventwrite.L.Lock()
	n, err = r.write(p)
	r.eventwrite.L.Unlock()
	r.sendeventwrite(n)
	return n, err
}

func (r *RingBuffer[T]) WriteWait(p []T) {
	totalbytes := len(p)
	if totalbytes == 0 {
		return
	}
	var cntwrite, cnt int
	cntwrite = 0

	for cntwrite != totalbytes {
		r.eventwrite.L.Lock()
		if r.isfull() {
			r.eventwrite.L.Unlock()
			r.eventbroacastread.L.Lock()
			r.eventbroacastread.Wait()
			r.eventbroacastread.L.Unlock()
			r.eventwrite.L.Lock()
		}
		if cnt, _ = r.write(p[cntwrite:]); cnt != 0 {
			cntwrite += cnt
			r.sendeventwrite(cnt)
		}
		r.eventwrite.L.Unlock()
	}
	// r.sendeventwrite(totalbytes)
	// return
}

// TryWrite writes len(p) bytes from p to the underlying buf like Write, but it is not blocking.
// If it has not succeeded to accquire the lock, it return 0 as n and ErrAccuqireLock.
func (r *RingBuffer[T]) TryWrite(p []T) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	ok := r.eventwrite.L.(*sync.Mutex).TryLock()
	if !ok {
		return 0, ErrAccuqireLock
	}

	n, err = r.write(p)
	r.eventwrite.L.Unlock()
	r.sendeventwrite(n)
	return n, err
}

// Push writes one element into buffer, and returns ErrIsFull if buffer is full.
func (r *RingBuffer[T]) Push(c T) error {
	r.eventwrite.L.Lock()
	err := r.writeElement(c)
	r.eventwrite.L.Unlock()
	if err == nil {
		r.sendeventread(1)
	}
	return err
}

func (r *RingBuffer[T]) PushForce(c T) {
	r.eventwrite.L.Lock()
	if r.isfull() {
		r.readElement() // remove element
	}
	r.writeElement(c)
	r.eventwrite.L.Unlock()
	r.sendeventwrite(1)
}

// Wait for buffer is not full then Push writes one element into buffer.
func (r *RingBuffer[T]) PushWait(c T) {
	r.eventwrite.L.Lock()
	for r.isfull() {
		r.eventwrite.Wait()
	}
	r.writeElement(c)
	r.eventwrite.L.Unlock()
	r.sendeventwrite(1)
}

// Wait for buffer is not full then Push writes one element into buffer.
// Not recommended for use
func (r *RingBuffer[T]) PushWaitTimeOutOld(c T, timeout time.Duration) (err error) {
	timeoutAt := time.Now().Add(timeout)
	stepSleep := timeout / time.Nanosecond / 10
	for {
		r.eventwrite.L.Lock()
		if r.isfull() {
			r.eventwrite.L.Unlock()
			if time.Now().After(timeoutAt) {
				return os.ErrDeadlineExceeded
			} else {
				runtime.Gosched()
				time.Sleep(stepSleep)
			}
		} else {
			break
		}
	}
	r.writeElement(c)
	r.eventwrite.L.Unlock()
	r.sendeventwrite(1) //signal for write new data
	return
}

func (r *RingBuffer[T]) PushWaitTimeOut(c T, timeout time.Duration) error {
	timeoutCh := time.After(timeout)
	pushSuccess := make(chan bool, 1)
	isTimeOut := false
	go func() {
		r.eventwrite.L.Lock()
		defer func() {
			r.eventwrite.L.Unlock()
		}()
		for r.isfull() {
			r.eventbroacastread.L.Lock()
			r.eventbroacastread.Wait() // Wait until the buffer is not full or a signal is received
			r.eventbroacastread.L.Unlock()
			if isTimeOut {
				return
			}
		}
		// After being signalled, check if we've timed out before writing to the buffer
		if isTimeOut {
			return
		}
		r.writeElement(c)
		r.sendeventwrite(1)
		pushSuccess <- true
	}()

	select {
	case <-pushSuccess:
		// The data was successfully pushed to the buffer
		return nil
	case <-timeoutCh: // If the timeout occurs before the goroutine is done
		isTimeOut = true
		r.eventbroacastread.Broadcast() // Wake up all goroutines waiting on r.eventbroacastread
		return os.ErrDeadlineExceeded
	}
}

func (r *RingBuffer[T]) WriteWaitTimeOut(c []T, timeout time.Duration, ctxs ...context.Context) (n int, err error) {
	if timeout <= 0 {
		timeout = 1<<63 - 1
	}
	timeoutCh := time.After(timeout)
	pushSuccess := make(chan bool, 1)
	isTimeOut := false
	go func() {
		r.eventwrite.L.Lock()
		defer func() {
			r.eventwrite.L.Unlock()
		}()
		for r.isfull() {
			r.eventbroacastread.L.Lock()
			r.eventbroacastread.Wait() // Wait until the buffer is not full or a signal is received
			r.eventbroacastread.L.Unlock()
			if isTimeOut {
				return
			}
		}
		// After being signalled, check if we've timed out before writing to the buffer
		if isTimeOut {
			return
		}
		n, err = r.write(c)
		r.sendeventwrite(n)
		pushSuccess <- true
	}()

	ctx := context.Background()
	if len(ctxs) > 0 {
		ctx = ctxs[0]
	}
	select {
	case <-pushSuccess:
		// The data was successfully pushed to the buffer
		return
	case <-ctx.Done():
		isTimeOut = true
		r.eventbroacastread.Broadcast() // Wake up all goroutines waiting on r.eventbroacastread
		err = ctx.Err()
		return

	case <-timeoutCh: // If the timeout occurs before the goroutine is done
		isTimeOut = true
		r.eventbroacastread.Broadcast() // Wake up all goroutines waiting on r.eventbroacastread
		err = os.ErrDeadlineExceeded
		return
	}
}

// TrywriteElementElement writes one element into buffer without blocking.
// If it has not succeeded to accquire the lock, it return ErrAccuqireLock.
func (r *RingBuffer[T]) TryPush(c T) error {
	ok := r.eventwrite.L.(*sync.Mutex).TryLock()
	if !ok {
		return ErrAccuqireLock
	}

	err := r.writeElement(c)
	r.eventwrite.L.Unlock()
	if err == nil {
		r.sendeventwrite(1)
	}
	return err
}

// Length return the length of available read bytes.
func (r *RingBuffer[T]) Length() int {
	r.eventwrite.L.(*sync.RWMutex).RLock()
	defer r.eventwrite.L.(*sync.RWMutex).RUnlock()
	return r.length()
}

func (r *RingBuffer[T]) length() int {
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
	r.eventwrite.L.(*sync.RWMutex).RLock()
	defer r.eventwrite.L.(*sync.RWMutex).RUnlock()
	return r.free()
}

// return number free elements
func (r *RingBuffer[T]) free() int {
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
	r.eventwrite.L.(*sync.RWMutex).RLock()
	defer r.eventwrite.L.(*sync.RWMutex).RUnlock()

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
	r.eventwrite.L.(*sync.RWMutex).RLock()
	defer r.eventwrite.L.(*sync.RWMutex).RUnlock()
	return r.isfull()
}

// resize resizes the deque to fit exactly twice its current contents.  This is
// used to grow the queue when it is full, and also to shrink it when it is
// only a quarter full.
func (r *RingBuffer[T]) Resize() (changed bool) {
	r.eventwrite.L.Lock()
	defer r.eventwrite.L.Unlock()
	return r.resize()
}

func (r *RingBuffer[T]) resize() (changed bool) {

	if r.size >= r.maxsize || r.size <= r.maxsize/8 {
		return
	}
	var newsize int
	lenbuff := r.length()
	if lenbuff == 0 {
		newsize = r.maxsize / 8 //Get 1/8 of max size
	} else {
		newsize = lenbuff + lenbuff/2 //Add 1/2 old size
		if newsize > r.maxsize {
			newsize = r.maxsize
		}
	}

	if newsize <= r.size {
		r.buf = r.buf[:newsize]
		return //no need resize
	}
	newBuf := make([]T, newsize)
	if r.isfull() {
		r.w++
		r.isFull = false
	}
	r.size = newsize
	copy(newBuf, r.buf)
	return true
}

// IsEmpty returns this ringbuffer is empty.
func (r *RingBuffer[T]) IsEmpty() bool {
	r.eventwrite.L.(*sync.RWMutex).RLock()
	defer r.eventwrite.L.(*sync.RWMutex).RUnlock()
	return r.isempty()
}

func (r *RingBuffer[T]) isempty() bool {
	return !r.isFull && r.w == r.r
}

func (r *RingBuffer[T]) isfull() bool {
	return r.isFull && r.w == r.r
}

// Reset the read pointer and writer pointer to zero.
func (r *RingBuffer[T]) Reset() {
	r.eventwrite.L.Lock()
	defer r.eventwrite.L.Unlock()
	r.sendeventread(r.length())
	r.r = 0
	r.w = 0
	r.isFull = false
}
