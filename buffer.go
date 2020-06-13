// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"fmt"
	"strconv"
	"strings"
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
	cleaning  bool      // If periodic or one-off cleaning is in progress
	done      chan bool // Or nil if periodic cleaning not started
}

// NewBuffer creates a new buffer with no unsent messages
func NewBuffer() *Buffer {
	return &Buffer{
		unsentSet: false,
		buf:       make([]*Envelope, 0),
		mx:        sync.Mutex{},
		cleaning:  false,
		done:      nil,
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

// TakeOver the envelopes of another buffer, which will be empty
func (b *Buffer) TakeOver(old *Buffer) {
	b.mx.Lock()
	defer b.mx.Unlock()

	b.buf = old.buf
	old.buf = make([]*Envelope, 0)
}

// Start a goroutine to periodically clean the buffer
func (b *Buffer) Start() {
	fLog := aLog.New("fn", "buffer.Start", "ref", &b)
	// Only start once at a time
	if !b.trySetPeriodicCleaning() {
		return
	}

	WG.Add(1)
	fLog.Debug("Started cleaning", "b.done", b.done)
	go func() {
		defer WG.Done()
		defer fLog.Debug("Done buffer 2")
		defer b.unsetCleaning()
		defer fLog.Debug("Done buffer 1")

		tickC := time.Tick(reconnectionTimeout / 4)
	cleaning:
		for {
			select {
			case <-tickC:
				b.cleanReal()
			case <-b.done:
				break cleaning
			}
		}
	}()
}

// tryStartCleaning tries to set cleaning to true, if it's false, and
// returns its success.
func (b *Buffer) trySetCleaning() bool {
	b.mx.Lock()
	defer b.mx.Unlock()
	if b.cleaning {
		return false
	}
	b.cleaning = true
	return true
}

// tryStartPeriodicCleaning tries to set periodic cleaning to true,
// if not already cleaning, and // returns its success.
func (b *Buffer) trySetPeriodicCleaning() bool {
	b.mx.Lock()
	defer b.mx.Unlock()
	if b.cleaning {
		return false
	}
	b.cleaning = true
	b.done = make(chan bool, 1)
	return true
}

// Unset periodic and one-off cleaning
func (b *Buffer) unsetCleaning() {
	b.mx.Lock()
	b.cleaning = false
	b.done = nil
	b.mx.Unlock()
}

func (b *Buffer) isCleaning() bool {
	b.mx.Lock()
	defer b.mx.Unlock()
	return b.cleaning
}

// Clean the buffer, leaving envelopes within the last
// `reconnectionTimeout`. Does nothing if periodic cleaning running
func (b *Buffer) Clean() {
	if !b.trySetCleaning() {
		return
	}
	b.cleanReal()
	b.unsetCleaning()
}

// The real cleaning process
func (b *Buffer) cleanReal() {
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
	b.mx.Lock()
	defer b.mx.Unlock()
	fLog := aLog.New("fn", "buffer.Stop", "ref", &b)
	fLog.Debug("Stopping buffer", "b.done", b.done)
	if b.done != nil {
		fLog.Debug("Sending stop for buffer")
		b.done <- true
	}
}

// String representation of the buffer.
func (b *Buffer) String() string {
	b.mx.Lock()
	defer b.mx.Unlock()

	nums := make([]string, len(b.buf))
	for i, env := range b.buf {
		nums[i] = strconv.Itoa(env.Num)
	}

	return "{unsent:" + strconv.Itoa(b.unsent) +
		",nums:[" + strings.Join(nums, ",") + "]}"
}
