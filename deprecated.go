package goring

// Deprecated: Use NewRingMultipleReader instead.
func NewRingMutipleReader[T any](size uint32, maxConsumers uint32) (*RingMultipleReader[T], error) {
	return NewRingMultipleReader[T](size, maxConsumers)
}
