// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"fmt"
	"time"
)

// Buffer holds envelopes for each client (by ID) which may need to be
// sent or resent at a later time.
type Buffer struct {
	buf map[string][]*Envelope
}

// Queue holds a queue of envelopes from some num onwards.
type Queue struct {
	q []*Envelope
}

// NewBuffer creates a new buffer with no unsent messages
func NewBuffer() *Buffer {
	return &Buffer{
		buf: make(map[string][]*Envelope, 0),
	}
}

// Add an envelope for a given client.
func (b *Buffer) Add(id string, e *Envelope) {
	es, ok := b.buf[id]
	if !ok {
		b.buf[id] = []*Envelope{e}
		return
	}
	b.buf[id] = append(es, e)
}

// Clean the buffer of all envelopes older than reconnectionTimeout
// (plus a bit for safety).
func (b *Buffer) Clean() {
	keep := time.Now().Add(reconnectionTimeout * -11 / 10)
	keepMs := keep.UnixNano() / 1_000_000
	for id, es := range b.buf {
		for i := range es {
			if es[i].Time >= keepMs {
				b.buf[id] = es[i:]
				break
			}
		}
	}
}

// Queue extracts a queue from a given num onwards, for some client ID.
func (b *Buffer) Queue(id string, num int) *Queue {
	es, ok := b.buf[id]
	if !ok {
		return NewQueue()
	}
	for i := range es {
		if es[i].Num == num {
			from := b.buf[id][i:]
			to := make([]*Envelope, len(from))
			copy(to, from)
			return &Queue{q: to}
		}
	}
	return NewQueue()
}

// Available says if a specific num envelope is available for some client ID.
func (b *Buffer) Available(id string, num int) bool {
	es, ok := b.buf[id]
	if !ok {
		return false
	}
	for i := range es {
		if es[i].Num == num {
			return true
		}
	}
	return false
}

// Remove all the entries of a given client ID
func (b *Buffer) Remove(id string) {
	delete(b.buf, id)
}

// NewQueue returns a new and empty queue.
func NewQueue() *Queue {
	return &Queue{
		q: []*Envelope{},
	}
}

// Get the first item in the queue, or return an error.
func (q *Queue) Get() (*Envelope, error) {
	if len(q.q) == 0 {
		return nil, fmt.Errorf("Queue is empty")
	}
	e := q.q[0]
	q.q = q.q[1:]
	return e, nil
}

// Add an envelope to the back of the queue.
func (q *Queue) Add(e *Envelope) {
	q.q = append(q.q, e)
}

// Empty tests if the queue is empty
func (q *Queue) Empty() bool {
	return len(q.q) == 0
}
