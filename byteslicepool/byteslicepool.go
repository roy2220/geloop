// Package byteslicepool wraps a sync.Pool for byte slices.
package byteslicepool

import "sync"

// Get a byte slice from the pool.
func Get() []byte {
	return bsp.Get().([]byte)
}

// Put the given byte slice to the pool.
func Put(bs []byte) {
	bsp.Put(bs[:0])
}

var bsp = sync.Pool{New: func() interface{} { return ([]byte)(nil) }}
