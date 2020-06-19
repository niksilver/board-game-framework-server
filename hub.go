// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"time"

	"github.com/gorilla/websocket"
)

// Hub collects all related clients
type Hub struct {
	clients map[*Client]bool
	// Current envelope num
	num int
	// Messages from clients that need to be bounced out.
	Pending chan *Message
	// Message from the superhub saying timed out waiting for a reconnection
	// to replace a client
	Timeout chan *TimeoutMsg
	// Buffer of recent envelopes, in case they need to be resent
	buffer *Buffer
}

// Message is something that the Hub needs to bounce out to clients
// other than the sender.
type Message struct {
	From  *Client
	MType int
	Env   *Envelope
}

// TimeoutMsg comes from the superhub, signalling that a client
// reconnection timeout has occurred.
type TimeoutMsg struct {
	Client    *Client // The client whose timer has fired
	Remaining int     // Number of clients still due to time out
}

// Envelope is the structure for messages sent to clients. Other than
// the bare minimum,
// all fields will be filled in by the hub. The fields have to be exported
// to be processed by json marshalling.
type Envelope struct {
	From   []string // Client id this is from
	To     []string // Ids of all clients this is going to
	Num    int      // A number reference for this envelope
	Time   int64    // Server time when sent, in seconds since the epoch
	Intent string   // What the message is intended to convey
	Body   []byte   // Original raw message from the sending client
}

// NewHub creates a new, empty Hub.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[*Client]bool),
		num:     0,
		Pending: make(chan *Message),
		Timeout: make(chan *TimeoutMsg),
		buffer:  NewBuffer(),
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

readingLoop:
	for {
		fLog.Debug("Selecting")

		select {
		case msg := <-h.Timeout:
			// The superhub's client reconnection timer has fired
			c := msg.Client
			caseLog := fLog.New("cid", c.ID, "cref", c.Ref)
			caseLog.Debug("Reconnection timed out")
			if cOther := h.other(c); cOther != nil {
				caseLog.Debug("Client has been replaced; ignoring",
					"cOtherID", cOther.ID, "cOtherRef", cOther.Ref)
				continue
			}

			// We have a leaver
			caseLog.Debug("Timed-out client becomes a leaver")

			if len(h.clients) == 0 && msg.Remaining == 0 {
				caseLog.Debug("No more clients timing out or in hub; exiting")
				break readingLoop
			}

			// Send a leaver message to remaining clients
			h.num++
			h.leaver(c)
			h.buffer.Remove(c.ID)
			caseLog.Debug("Sent leaver messages")

		case msg := <-h.Pending:
			fLog.Debug("Received pending message")

			switch {
			case msg.Env.Intent == "Joiner" &&
				!h.canFulfill(msg.From.ID, msg.From.Num):
				// New client but bad lastnum; eject client
				c := msg.From
				caseLog := fLog.New("fromcid", c.ID, "fromcref", c.Ref)
				caseLog.Debug("New client but bad lastnum")
				c.InitialQueue <- h.buffer.Queue(c.ID, c.Num)
				c.Pending <- &Message{
					Env: &Envelope{Intent: "BadLastnum"},
				}
				close(c.Pending)

			case msg.Env.Intent == "Joiner" &&
				h.other(msg.From) != nil &&
				h.canFulfill(msg.From.ID, msg.From.Num):
				// New client taking over from old client
				c := msg.From
				caseLog := fLog.New("fromcid", c.ID, "fromcref", c.Ref)
				cOld := h.other(msg.From)
				caseLog.Debug("New client taking over", "oldcref", cOld.Ref)

				// Give the new client its initial queue to start it off
				c.InitialQueue <- h.buffer.Queue(c.ID, c.Num)

				// Add the client to our list
				h.clients[c] = true

				// Shut down old client
				h.removeSafely(cOld)

			case msg.Env.Intent == "Joiner" &&
				h.other(msg.From) != nil &&
				msg.From.Num < 0:
				// New client for old ID, but didn't ask to take over
				c := msg.From
				caseLog := fLog.New("newcid", c.ID, "newcref", c.Ref)
				cOld := h.other(msg.From)
				caseLog.Debug("New client while old present, but no takeover",
					"oldcref", cOld.Ref)
				h.removeSafely(cOld)
				caseLog.Debug("Sending leaver messages")
				h.num++
				h.leaver(cOld)
				h.buffer.Remove(cOld.ID)

				caseLog.Debug("Sending joiner messages")
				h.num++
				h.joiner(c)

				// Set the new client going with an empty queue, send it
				// a welcome message, and add it to our client list
				c.InitialQueue <- NewQueue()
				caseLog.Debug("Sending welcome message")
				h.welcome(c)
				h.clients[c] = true

			case msg.Env.Intent == "Joiner" && h.other(msg.From) == nil:
				// New joiner
				c := msg.From
				caseLog := fLog.New("fromcid", c.ID, "fromcref", c.Ref)

				// Send joiner message to other clients
				h.num++
				caseLog.Debug("Sending joiner messages")
				h.joiner(c)

				// Set the new client going with an empty queue, send it
				// a welcome message, and add it to our client list
				c.InitialQueue <- NewQueue()
				caseLog.Debug("Sending welcome message")
				h.welcome(c)
				h.clients[c] = true

			case msg.Env.Intent == "LostConnection":
				// A client receiver has lost the connection
				c := msg.From
				fLog.Debug("Got lost connection",
					"fromcid", c.ID, "fromcref", c.Ref)
				h.removeSafely(c)

			case msg.Env.Intent == "Peer":
				// We have a peer message
				c := msg.From
				caseLog := fLog.New("fromcid", c.ID, "fromcref", c.Ref)
				caseLog.Debug("Got peer msg", "content", string(msg.Env.Body))

				toCls := h.exclude(c)
				h.num++
				msg.Env.From = []string{c.ID}
				msg.Env.To = ids(toCls)
				msg.Env.Num = h.num
				msg.Env.Time = time.Now().Unix()
				msg.Env.Intent = "Peer"

				caseLog.Debug("Sending peer messages")
				for _, cl := range toCls {
					caseLog.Debug("Sending peer msg", "tocid", cl.ID)
					h.buffer.Add(cl.ID, msg.Env)
					cl.Pending <- msg
				}

				caseLog.Debug("Sending receipt")
				msgR := &Message{
					From: c,
					Env: &Envelope{
						From:   msg.Env.From,
						To:     msg.Env.To,
						Num:    msg.Env.Num,
						Time:   msg.Env.Time,
						Intent: "Receipt",
						Body:   msg.Env.Body,
					},
				}
				h.buffer.Add(c.ID, msgR.Env)
				c.Pending <- msgR

			default:
				// Should never get here
				fLog.Error("Cannot handle message", "msg", msg)
			}
			h.buffer.Clean()
		}

	}
}

// canFullfill says if we can send the next num the client is expecting
func (h *Hub) canFulfill(id string, num int) bool {
	return num < 0 || num == h.num || h.buffer.Available(id, num)
}

// remove a given client
func (h *Hub) removeSafely(c *Client) {
	if h.clients[c] {
		close(c.Pending)
	}
	delete(h.clients, c)
}

// welcome sends a Welcome message to just this client.
func (h *Hub) welcome(c *Client) {
	msg := &Message{
		From:  c,
		MType: websocket.BinaryMessage,
		Env: &Envelope{
			To:     []string{c.ID},
			From:   h.allIDs(),
			Num:    h.num,
			Time:   time.Now().Unix(),
			Intent: "Welcome",
		},
	}
	h.buffer.Add(c.ID, msg.Env)
	c.Pending <- msg
}

// joiner sends a Joiner message to all clients, about joiner c.
func (h *Hub) joiner(c *Client) {
	msg := &Message{
		From:  c,
		MType: websocket.BinaryMessage,
		Env: &Envelope{
			From:   []string{c.ID},
			To:     h.allIDs(),
			Num:    h.num,
			Time:   time.Now().Unix(),
			Intent: "Joiner",
		},
	}

	for cl, _ := range h.clients {
		h.buffer.Add(cl.ID, msg.Env)
		cl.Pending <- msg
	}
}

// leaver message sent to all clients, about leaver c.
func (h *Hub) leaver(c *Client) {
	msg := &Message{
		From:  c,
		MType: websocket.BinaryMessage,
		Env: &Envelope{
			From:   []string{c.ID},
			To:     h.allIDs(),
			Num:    h.num,
			Time:   time.Now().Unix(),
			Intent: "Leaver",
		},
	}
	for cl, _ := range h.clients {
		h.buffer.Add(cl.ID, msg.Env)
		cl.Pending <- msg
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
