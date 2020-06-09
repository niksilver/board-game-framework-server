// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"fmt"
	"sync"
	"time"
)

// Buffer holds envelopes which may need to be sent or resent at a
// later time.
type Buffer struct {
	unsent    int
	unsentSet bool
	buf       []*Envelope
	mx        sync.Mutex
	done      chan bool
}

// NewBuffer creates a new buffer with no unsent messages
func NewBuffer() *Buffer {
	return &Buffer{
		unsentSet: false,
		buf:       make([]*Envelope, 0),
		mx:        sync.Mutex{},
		done:      make(chan bool),
	}
}

// HasUnsent says if there is an unsent message expected and present
func (b *Buffer) HasUnsent() bool {
	b.mx.Lock()
	defer b.mx.Unlock()

	if !b.unsentSet {
		return false
	}

	for _, env := range b.buf {
		if env.Num == b.unsent {
			return true
		}
	}
	return false
}

// Set the num of the next unsent envelope we expect to see. Nums are
// sequential, so after receiving envelope 123 we'd expect to see 124.
func (b *Buffer) Set(num int) {
	b.mx.Lock()
	b.unsent = num
	b.unsentSet = true
	b.mx.Unlock()
}

// Next gets the next unsent message expected, and updates the num
// accordingly. Returns an error if the expected envelope is not in the
// buffer
func (b *Buffer) Next() (*Envelope, error) {
	b.mx.Lock()
	defer b.mx.Unlock()

	if !b.unsentSet {
		return nil, fmt.Errorf("No num set for next unsent envelope")
	}

	for _, env := range b.buf {
		if env.Num == b.unsent {
			b.unsent++
			return env, nil
		}
	}
	return nil, fmt.Errorf("Envelope num %d not in buffer", b.unsent)
}

// Add an envelope into the buffer. Envelopes should have sequential
// nums, otherwise eventually the next envelope will not be found.
func (b *Buffer) Add(env *Envelope) {
	b.mx.Lock()
	defer b.mx.Unlock()

	b.buf = append(b.buf, env)
}

// Start a goroutine to periodically clean the buffer
func (b *Buffer) Start() {
	WG.Add(1)
	go func() {
		defer WG.Done()
		tickC := time.Tick(reconnectionTimeout / 4)
	cleaning:
		for {
			select {
			case <-tickC:
				b.Clean()
			case <-b.done:
				break cleaning
			}
		}
	}()
}

// Clean the buffer, leaving envelopes within the last
// `reconnectionTimeout`.
func (b *Buffer) Clean() {

	keep := time.Now().Add(reconnectionTimeout * -11 / 10)
	keepMs := keep.UnixNano() / 1_000_000
	for i := range b.buf {
		if b.buf[i].Time >= keepMs {
			b.mx.Lock()
			b.buf = b.buf[i:]
			b.mx.Unlock()
			break
		}
	}
}

// Stop the periodic cleaning goroutine
func (b *Buffer) Stop() {
	b.done <- true
}
