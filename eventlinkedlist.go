package goring

import (
	"fmt"
	"reflect"
	"sync"
)

type EventLinkedList[T any] struct {
	buf []T
	r   int // next position to read
	// mu  Mutex
	// closed bool
	cond *sync.Cond
}

func NewEventLinkedList[T any]() *EventLinkedList[T] {
	return &EventLinkedList[T]{
		buf:  make([]T, 0),
		r:    0,
		cond: sync.NewCond(new(sync.Mutex)),
	}
}

func (pl *EventLinkedList[T]) Length() int {
	pl.cond.L.Lock()
	defer pl.cond.L.Unlock()
	return len(pl.buf)
}

func (pl *EventLinkedList[T]) String() string {
	pl.cond.L.Lock()
	defer pl.cond.L.Unlock()
	return fmt.Sprintf("Length: %d\nCurrent read: %d", len(pl.buf), pl.r)
}

func (pl *EventLinkedList[T]) Reset() {
	pl.cond.L.Lock()
	defer pl.cond.L.Unlock()
	pl.buf = make([]T, 0)
	pl.r = 0
}

func (pl *EventLinkedList[T]) UpdateNewEventLinkedList(p []T) (changed bool) {
	pl.cond.L.Lock()
	defer func() {
		pl.cond.L.Unlock()
		pl.cond.Broadcast()
	}()
	if !reflect.DeepEqual(pl.buf, p) {
		lenp := len(p)
		pl.buf = make([]T, lenp)
		copy(pl.buf, p)
		pl.r = 0
		return true
	} else {
		return false
	}
}

func (pl *EventLinkedList[T]) seek(wait bool, step int) (T, error) {
	var zero T
	pl.cond.L.Lock()
	defer pl.cond.L.Unlock()
	if !wait && len(pl.buf) == 0 {
		return zero, ErrIsEmpty
	} else {
		for len(pl.buf) == 0 && wait {
			pl.cond.Wait()
		}
		lenbuf := len(pl.buf)
		// if (step >= 0 && step <= lenbuf) || (step < 0 && -step <= lenbuf) {
		delta := pl.r + lenbuf + step
		if delta < 0 {
			delta = delta - (-delta/lenbuf+1)*delta
		}
		pl.r = delta % lenbuf
		return pl.buf[pl.r], nil
	}
}

func (pl *EventLinkedList[T]) SeekWait(n int) (T, error) {
	return pl.seek(true, n)
}

func (pl *EventLinkedList[T]) Seek(n int) (T, error) {
	return pl.seek(false, n)
}

func (pl *EventLinkedList[T]) NextWait() (T, error) {
	return pl.seek(true, 1)
}

func (pl *EventLinkedList[T]) Next() (T, error) {
	return pl.seek(false, 1)
}

func (pl *EventLinkedList[T]) Prev() (T, error) {
	return pl.seek(false, -1)
}
func (pl *EventLinkedList[T]) PrevWait() (T, error) {
	return pl.seek(true, -1)
}

func (pl *EventLinkedList[T]) Copy() ([]T, error) {
	pl.cond.L.Lock()
	defer pl.cond.L.Unlock()
	lenbuf := len(pl.buf)
	if lenbuf == 0 {
		return []T{}, ErrIsEmpty
	}
	retbuf := make([]T, lenbuf)
	copy(retbuf, pl.buf)
	return retbuf, nil
}

func (pl *EventLinkedList[T]) Current() (T, error) {
	var zero T
	pl.cond.L.Lock()
	defer pl.cond.L.Unlock()
	lenbuf := len(pl.buf)
	if lenbuf == 0 {
		return zero, ErrIsEmpty
	} else {
		return pl.buf[pl.r], nil
	}
}

func (pl *EventLinkedList[T]) _seek(n int) (T, error) {
	var zero T
	pl.cond.L.Lock()
	defer pl.cond.L.Unlock()
	lenbuf := len(pl.buf)
	if lenbuf == 0 {
		return zero, ErrIsEmpty
	} else {
		if (n >= 0 && n <= lenbuf) || (n < 0 && -n <= lenbuf) {
			pl.r = (pl.r + lenbuf + n) % lenbuf
			return pl.buf[pl.r], nil
		} else {
			return zero, ErrOverflow
		}
	}
}

func (pl *EventLinkedList[T]) Remove(n int) error {
	pl.cond.L.Lock()
	defer pl.cond.L.Unlock()
	lenbuf := len(pl.buf)
	if lenbuf == 0 {
		return ErrIsEmpty
	} else {
		if n >= 0 && n < lenbuf {
			if lenbuf == 1 {
				pl.buf = make([]T, 0)
			} else {
				copy(pl.buf[n:], pl.buf[n+1:])
				pl.buf = pl.buf[:lenbuf-1]
			}
			return nil
		} else {
			return ErrOverflow
		}
	}
}

func (pl *EventLinkedList[T]) Insert(n int, els ...T) error {
	pl.cond.L.Lock()
	defer func() {
		pl.cond.L.Unlock()
		pl.cond.Broadcast()
	}()
	lenbuf := len(pl.buf)
	if lenbuf == 0 || len(els) == 0 {
		return ErrIsEmpty
	} else {
		if n >= 0 && n < lenbuf {
			lenels := len(els)
			if lenbuf == 1 {
				pl.buf = append(els, pl.buf[0])
			} else {
				newbuf := make([]T, lenbuf+lenels)
				if n == 0 {
					copy(newbuf, els)
					copy(newbuf[lenels:], pl.buf[1:])
				} else if n == lenbuf-1 {
					ene := pl.buf[lenbuf-1]
					pl.buf = append(pl.buf, els...)
					pl.buf = append(pl.buf, ene)
					return nil
				} else {
					copy(newbuf, pl.buf[:n-1])
					copy(newbuf[n:], els)
					copy(newbuf[lenbuf+lenels:], els)
				}
				pl.buf = newbuf
			}
			return nil
		} else {
			return ErrOverflow
		}
	}
}
