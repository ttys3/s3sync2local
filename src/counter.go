package main

import (
	"sync/atomic"
)

type Counter interface {
	Count() int64
	Inc()
	Add(count int64)
	Clear()
}

func NewCounter() Counter {
	return &StandardCounter{
		count: 0,
	}
}

type StandardCounter struct {
	count int64
}

func (c *StandardCounter) Count() int64 {
	return atomic.LoadInt64(&c.count)
}

func (c *StandardCounter) Inc() {
	atomic.AddInt64(&c.count, 1)
}

func (c *StandardCounter) Add(count int64) {
	atomic.AddInt64(&c.count, count)
}

func (c *StandardCounter) Clear() {
	atomic.StoreInt64(&c.count, 0)
}