package goring

// RingBytes is a circular buffer that implement io.Reader, io.ByteReader , io.ByteWriter, io.ReadWriter interface.
type RingBytes struct {
	rb *RingBuffer[byte]
}

// New returns a new RingBytes whose buffer has the given size.
func NewRingBytes(size int) *RingBytes {
	return &RingBytes{
		NewRing[byte](size),
	}
}

// Read reads up to len(p) bytes into p. It returns the number of bytes read (0 <= n <= len(p)) and any error encountered. Even if Read returns n < len(p), it may use all of p as scratch space during the call. If some data is available but not len(p) bytes, Read conventionally returns what is available instead of waiting for more.
// When Read encounters an error or end-of-file condition after successfully reading n > 0 bytes, it returns the number of bytes read. It may return the (non-nil) error from the same call or return the error (and n == 0) from a subsequent call.
// Callers should always process the n > 0 bytes returned before considering the error err. Doing so correctly handles I/O errors that happen after reading some bytes and also both of the allowed EOF behaviors.
func (r *RingBytes) Read(p []byte) (n int, err error) {
	return r.rb.Read(p)
}

// TryRead read up to len(p) bytes into p like Read but it is not blocking.
// If it has not succeeded to accquire the lock, it return 0 as n and ErrAccuqireLock.
func (r *RingBytes) TryRead(p []byte) (n int, err error) {
	return r.rb.TryRead(p)
}

// ReadByte reads and returns the next byte from the input or ErrIsEmpty.
func (r *RingBytes) ReadByte() (b byte, err error) { //implement io.ByteReader
	return r.rb.Pop()
}

// Write writes len(p) bytes from p to the underlying buf.
// It returns the number of bytes written from p (0 <= n <= len(p)) and any error encountered that caused the write to stop early.
// Write returns a non-nil error if it returns n < len(p).
// Write must not modify the slice data, even temporarily.
// func (r *RingBytes) Write(p []byte) (n int, err error) {
// 	return r.Write(p)
// }
func (r *RingBytes) Write(p []byte) (n int, err error) {
	return r.rb.Write(p)
}

// TryWrite writes len(p) bytes from p to the underlying buf like Write, but it is not blocking.
// If it has not succeeded to accquire the lock, it return 0 as n and ErrAccuqireLock.
func (r *RingBytes) TryWrite(p []byte) (n int, err error) {
	return r.rb.TryWrite(p)
}

// WriteByte writes one byte into buffer, and returns ErrIsFull if buffer is full.
func (r *RingBytes) WriteByte(c byte) error { //implement io.ByteWriter
	return r.rb.writeElement(c)
}

func (r *RingBytes) WriteString(s string) (n int, err error) {
	return r.rb.Write([]byte(s))
}

// TryWriteByte writes one byte into buffer without blocking.
// If it has not succeeded to accquire the lock, it return ErrAccuqireLock.
func (r *RingBytes) TryWriteByte(c byte) error {
	return r.rb.TryPush(c)
}

// Length return the length of available read bytes.
func (r *RingBytes) Length() int {
	return r.rb.Length()
}

// Capacity returns the size of the underlying buffer.
func (r *RingBytes) Capacity() int {
	return r.rb.Capacity()
}

// Free returns the length of available bytes to write.
func (r *RingBytes) Free() int {
	return r.rb.Free()
}

// Bytes returns all available read bytes. It does not move the read pointer and only copy the available data.
func (r *RingBytes) Bytes() []byte {
	return r.rb.Copy()
}

// IsFull returns this ringbuffer is full.
func (r *RingBytes) IsFull() bool {
	return r.rb.IsFull()
}

// IsEmpty returns this ringbuffer is empty.
func (r *RingBytes) IsEmpty() bool {
	return r.rb.IsEmpty()
}

// Reset the read pointer and writer pointer to zero.
func (r *RingBytes) Reset() {
	r.rb.Reset()
}

func (r *RingBytes) String() string {
	return r.rb.String()
}
