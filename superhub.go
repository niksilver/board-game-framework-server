// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"fmt"
	"sync"
	"time"
)

const MaxClients = 50

// Superhub gives a hub to a client. The client needs to
// release the hub when it's done with it.
type Superhub struct {
	hubs   map[string]*Hub    // From game name (path) to hub
	counts map[*Hub]int       // Count of clients using each hub
	names  map[*Hub]string    // From hub pointer to game name
	tOut   map[*Hub][]*Client // Clients timing out per hub
	mux    sync.RWMutex       // To ensure concurrency-safety
}

// newSuperhub creates an empty superhub, which will hold many hubs.
func NewSuperhub() *Superhub {
	return &Superhub{
		hubs:   make(map[string]*Hub),    // From game name to hub
		counts: make(map[*Hub]int),       // Count of cl's using a hub
		names:  make(map[*Hub]string),    // From hub ptr to game name
		tOut:   make(map[*Hub][]*Client), // Clients timing out per hub
		mux:    sync.RWMutex{},           // For concurrency-safety
	}
}

// Hub gets the hub for the given game name. If necessary a new hub
// will be created and start processing messages.
// Will return an error if there are too many clients in the game.
func (sh *Superhub) Hub(name string) (*Hub, error) {
	aLog.Debug("superhub.Hub, Entering", "name", name)
	sh.mux.Lock()
	defer sh.mux.Unlock()
	aLog.Debug("superhub.Hub, giving hub", "name", name)

	if h, okay := sh.hubs[name]; okay {
		if sh.counts[h] >= MaxClients {
			return nil, fmt.Errorf("Maximum number of clients in game")
		}
		sh.counts[h]++
		aLog.Debug("superhub.Hub, existing hub",
			"name", name, "count", sh.counts[h])
		return h, nil
	}

	aLog.Debug("superhub.Hub, new hub", "name", name)
	h := NewHub(name)
	sh.hubs[name] = h
	sh.counts[h] = 1
	sh.names[h] = name
	aLog.Debug("superhub.Hub, starting hub", "name", name)
	h.Start()
	aLog.Debug("superhub.Hub, exiting", "name", name)

	return h, nil
}

// Release allows a client to say it is no longer using the given hub.
// A reconnection timer will start and eventually alert the hub.
func (sh *Superhub) Release(h *Hub, c *Client) {
	sh.mux.Lock()
	defer sh.mux.Unlock()

	fLog := aLog.New("fn", "superhub.Release", "hubname", sh.names[h],
		"cid", c.ID, "cref", c.Ref)
	fLog.Debug("Starting reconnection timeout")

	// Put the client in the timing-out list
	sh.tOut[h] = append(sh.tOut[h], c)

	// Send a possible message to the hub after timeout
	time.AfterFunc(reconnectionTimeout,
		func() {
			sh.mux.Lock()
			defer sh.mux.Unlock()

			fLog := aLog.New("fn", "superhub.Release.AfterFunc",
				"hubname", sh.names[h], "hubref", h.ref, "cid", c.ID, "cref", c.Ref)
			fLog.Debug("Entering")
			// Delete the client from the list
			sh.tOut[h] = remove(sh.tOut[h], c)
			sh.decrement(h)
			// Send a timeout message to the hub
			hc := make(map[string]int)
			for h, c := range sh.counts {
				hc[sh.names[h]] = c
			}
			fLog.Debug("Hubs and counts", "map", hc)
			h.Timeout <- c
			// For testing only...
			fLog.Debug("Sent timeout for client")
		})

	fLog.Debug("Exiting")
}

// Decrement the count of clients for a hub, and remove the hub if necessary
func (sh *Superhub) decrement(h *Hub) {
	sh.counts[h]--
	if sh.counts[h] == 0 {
		aLog.Debug("superhub.decrement, deleting hub", "name", sh.names[h])
		delete(sh.hubs, sh.names[h])
		delete(sh.counts, h)
		delete(sh.names, h)
		delete(sh.tOut, h)
	}
}

// Remove one client from a slice of clients
func remove(cs []*Client, c *Client) []*Client {
	for i, c2 := range cs {
		if c == c2 {
			return append(cs[:i], cs[i+1:]...)
		}
	}
	return cs
}

// Count returns the number of hubs in the superhub
func (sh *Superhub) Count() int {
	sh.mux.RLock()
	defer sh.mux.RUnlock()

	for _, name := range sh.names {
		aLog.Debug("superhub.count, counting", "name", name)
	}
	return len(sh.names)
}
