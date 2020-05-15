package main

import (
	"sync/atomic"
)

type Counter interface {
	Count() uint64
	Inc()
	Add(count uint64)
	Clear()
}

func NewCounter() Counter {
	return &StandardCounter{
		count: uint64(0),
	}
}

type StandardCounter struct {
	count uint64
}

func (c *StandardCounter) Count() uint64 {
	return atomic.LoadUint64(&c.count)
}

func (c *StandardCounter) Inc() {
	atomic.AddUint64(&c.count, 1)
}

func (c *StandardCounter) Add(count uint64) {
	atomic.AddUint64(&c.count, count)
}

func (c *StandardCounter) Clear() {
	atomic.StoreUint64(&c.count, 0)
}