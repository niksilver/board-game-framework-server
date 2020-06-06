// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

// Hub collects all related clients
type Hub struct {
	//cMux    sync.RWMutex // For reading and writing clients
	clients map[*Client]bool
	// Messages from clients that need to be bounced out.
	Pending chan *Message
	// For the superhub to say there will be no more joiners
	Detached chan bool
	// For the hub to note to itself it's acknowledged the detachement
	detachedAck bool
}

// Message is something that the Hub needs to bounce out to clients
// other than the sender.
type Message struct {
	From  *Client
	MType int
	Env   *Envelope
}

// Envelope is the structure for messages sent to clients. Other than
// the bare minimum,
// all fields will be filled in by the hub. The fields have to be exported
// to be processed by json marshalling.
type Envelope struct {
	From   []string // Client id this is from
	To     []string // Ids of all clients this is going to
	Time   int64    // Server time when sent, in seconds since the epoch
	Intent string   // What the message is intended to convey
	Body   []byte   // Original raw message from the sending client
}

// NewHub creates a new, empty Hub.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[*Client]bool),
		Pending: make(chan *Message),
		// Channel size 1 so the superhub doesn't block
		Detached: make(chan bool, 1),
		// For the hub to note to itself it's acknowledged the detachement
		detachedAck: false,
	}
}

// Start starts goroutines running that process the messages.
func (h *Hub) Start() {
	aLog.Debug("hub.Start, adding for receiveInt")
	WG.Add(1)
	go h.receiveInt()
}

// receiveInt is a goroutine that listens for pending messages, and sends
// them out to the relevant clients.
func (h *Hub) receiveInt() {
	fLog := aLog.New("fn", "hub.receiveInt")

	defer fLog.Debug("Goroutine done")
	defer WG.Done()
	fLog.Debug("Entering")

	for !h.detachedAck {
		fLog.Debug("Selecting")

		select {
		case <-h.Detached:
			fLog.Debug("Received detached flag")
			h.detachedAck = true

		case msg := <-h.Pending:
			fLog.Debug("Received pending message")

			switch {
			case msg.Env.Intent == "Joiner" && h.other(msg.From) == nil:
				// New joiner
				c := msg.From

				// Send welcome message to joiner
				fLog.Debug("Sending welcome message", "fromcid", c.ID, "fromcref", c.Ref)
				c.Pending <- &Message{
					From:  c,
					MType: websocket.BinaryMessage,
					Env: &Envelope{
						To:     []string{c.ID},
						From:   h.allIDs(),
						Time:   time.Now().Unix(),
						Intent: "Welcome",
					},
				}

				// Send joiner message to other clients
				msg := &Message{
					From:  c,
					MType: websocket.BinaryMessage,
					Env: &Envelope{
						From:   []string{c.ID},
						To:     h.allIDs(),
						Time:   time.Now().Unix(),
						Intent: "Joiner",
					},
				}

				fLog.Debug("Sending joiner messages", "fromcid", c.ID, "fromcref", c.Ref)
				for cl, _ := range h.clients {
					fLog.Debug("Sending msg", "fromcid", c.ID, "tocid", cl.ID, "fromcref", c.Ref)
					cl.Pending <- msg
				}
				fLog.Debug("Sent joiner messages", "fromcid", c.ID, "fromcref", c.Ref)

				// Add the client to our list
				h.clients[c] = true

			case msg.Env.Intent == "Joiner":
				// A reconnection; new client with an old ID
				c := msg.From
				cOld := h.other(c)
				f2Log := fLog.New("fromcid", c.ID, "fromcref", c.Ref,
					"oldcid", cOld.ID, "oldcref", cOld.Ref)
				f2Log.Debug("Got reconnection")
				c.QueueC <- cOld.getQueue()
				f2Log.Debug("Closing old given reconnection")
				close(cOld.Pending)
				delete(h.clients, cOld)

			case msg.Env != nil && msg.Env.Intent == "LostConnection":
				// A client receiver has lost the connection
				c := msg.From
				fLog.Debug("Got lost connection", "fromcid", c.ID, "fromcref", c.Ref)
				c.Pending <- msg
				fLog.Debug("Sent lost connection message", "fromcid", c.ID, "fromcref", c.Ref)

			case msg.Env != nil && msg.Env.Intent == "ReconnectionTimeout":
				// There was no reconnection for a client
				c := msg.From
				fLog.Debug("Reconnection timed out", "fromcid", c.ID, "fromcref", c.Ref)
				if !h.clients[c] {
					fLog.Debug("Timed-out client gone", "fromcid", c.ID, "fromcref", c.Ref)
					continue
				}

				// We have a leaver
				fLog.Debug("Got a leaver", "fromcid", c.ID, "fromcref", c.Ref)

				// Tell the client it will receive no more messages and
				// forget about it
				fLog.Debug("Closing cl channel", "fromcid", c.ID, "fromcref", c.Ref)
				close(c.Pending)
				delete(h.clients, c)

				// Send a leaver message to all other clients
				msg := &Message{
					From:  c,
					MType: websocket.BinaryMessage,
					Env: &Envelope{
						From:   []string{c.ID},
						To:     h.allIDs(),
						Time:   time.Now().Unix(),
						Intent: "Leaver",
					},
				}
				fLog.Debug("Sending leaver messages")
				for cl, _ := range h.clients {
					fLog.Debug("Sending leaver msg",
						"fromcid", c.ID, "tocid", cl.ID)
					cl.Pending <- msg
				}
				fLog.Debug("Sent leaver messages")

			case msg.Env != nil && msg.Env.Body != nil:
				// We have a peer message
				c := msg.From
				fLog.Debug("Got peer msg", "fromcid", c.ID, "fromcref", c.Ref)

				toCls := h.exclude(c)
				msg.Env.From = []string{c.ID}
				msg.Env.To = ids(toCls)
				msg.Env.Time = time.Now().Unix()
				msg.Env.Intent = "Peer"

				fLog.Debug("Sending peer messages")
				for _, cl := range toCls {
					fLog.Debug("Sending peer msg",
						"fromcid", c.ID, "tocid", cl.ID)
					cl.Pending <- msg
				}
				fLog.Debug("Sent peer messages")

			default:
				// Should never get here
				panic(fmt.Sprintf("Got inexplicable msg: %#v", msg))
			}
		}

	}
}

// exclude finds all clients which aren't the given one.
// Matching is done on pointers.
func (h *Hub) exclude(cx *Client) []*Client {
	aLog.Debug("hub.exclude, entering")
	cOut := make([]*Client, 0)
	for c, _ := range h.clients {
		if c != cx {
			cOut = append(cOut, c)
		}
	}
	aLog.Debug("hub.exclude, exiting")
	return cOut
}

// allIDs returns all the IDs known to the hub
func (h *Hub) allIDs() []string {
	out := make([]string, 0)
	for c, _ := range h.clients {
		out = append(out, c.ID)
	}
	return out
}

// ids returns just the IDs of the clients
func ids(cs []*Client) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

// other returns the other client with the same ID, or nil
func (h *Hub) other(c *Client) *Client {
	var cOther *Client
	for k := range h.clients {
		if k.ID != c.ID {
			continue
		}
		if cOther != nil {
			panic("Found a second client with the same ID: " + c.ID)
		}
		cOther = k
	}
	return cOther
}
