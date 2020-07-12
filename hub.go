// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"time"
)

// Hub collects all related clients
type Hub struct {
	name string
	// All clients that have been seen, and the superhub is tracking
	clients map[*Client]status
	// Num for the next envelope num
	num int
	// Messages from clients that need to be bounced out.
	Pending chan *Message
	// Message from the superhub saying timed out waiting for a reconnection
	// to replace a client
	Timeout chan *Client
	// Buffer of recent envelopes, in case they need to be resent
	buffer *Buffer
}

// The status of any client seen, and that the superhub is tracking
type status int

// Various client statuses
const (
	// Client is connected
	CONNECTED status = 1
	// Client may reconnect, but is currently disconnected
	MAYRECONNECT status = 2
	// Client is being tracked, but is not known to other clients
	TRACKEDONLY status = 3
)

// Message is what is received from a Client.
type Message struct {
	From   *Client
	Intent string
	Body   []byte
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

// NewHub creates a new, empty Hub with a given name.
func NewHub(name string) *Hub {
	return &Hub{
		name:    name,
		clients: make(map[*Client]status),
		num:     0,
		Pending: make(chan *Message),
		Timeout: make(chan *Client),
		buffer:  NewBuffer(),
	}
}

// Start starts goroutines running that process the messages.
func (h *Hub) Start() {
	aLog.Debug("Adding for receiveInt", "fn", "hub.Start", "name", h.name)
	WG.Add(1)
	go h.receiveInt()
}

// receiveInt is a goroutine that listens for pending messages, and sends
// them to the connected clients, buffers them for all known clients.
func (h *Hub) receiveInt() {
	fLog := aLog.New("fn", "hub.receiveInt", "name", h.name)

	defer fLog.Debug("Goroutine done")
	defer WG.Done()
	fLog.Debug("Entering")

readingLoop:
	for {
		fLog.Debug("Selecting")

		select {
		case c := <-h.Timeout:
			// The superhub's client reconnection timer has fired
			caseLog := fLog.New("cid", c.ID, "cref", c.Ref)
			caseLog.Debug("Reconnection timed out")

			if h.stillJoined(c) {
				// We have a leaver
				h.remove(c)
				h.leaver(c)
				h.num++
				caseLog.Debug("Sent leaver messages")
			} else {
				caseLog.Debug("No messages to send")
				h.remove(c)
			}

			if len(h.clients) == 0 {
				caseLog.Debug("That was the last client; exiting")
				break readingLoop
			}

		case msg := <-h.Pending:
			fLog.Debug("Received pending message")

			switch {
			case msg.Intent == "Joiner" &&
				!h.canFulfill(msg.From.ID, msg.From.Num):
				// New client but bad lastnum; tell the client and then
				// just track it quietly
				c := msg.From
				caseLog := fLog.New("cid", c.ID, "cref", c.Ref)
				caseLog.Debug("New client but bad num", "num", msg.From.Num)

				// Tell the client there's an error
				h.connect(c, NewQueue())
				c.Pending <- &Envelope{Intent: "BadLastnum"}
				h.justTrack(c)

			case msg.Intent == "Joiner" &&
				h.otherJoined(msg.From) != nil &&
				msg.From.Num >= 0 &&
				h.canFulfill(msg.From.ID, msg.From.Num):
				// New client taking over from old client
				c := msg.From
				caseLog := fLog.New("cid", c.ID, "cref", c.Ref)
				cOld := h.otherJoined(msg.From)
				caseLog.Debug("New client taking over", "oldcref", cOld.Ref)

				// Let the new client replace the old client and start it off
				h.replace(c, h.buffer.Queue(c.ID, c.Num), cOld)

			case msg.Intent == "Joiner" &&
				h.otherJoined(msg.From) != nil &&
				msg.From.Num < 0:
				// New client for old ID, but didn't ask to take over
				c := msg.From
				cOld := h.otherJoined(msg.From)
				caseLog := fLog.New("newcid", c.ID, "newcref", c.Ref,
					"oldcref", cOld.Ref)
				caseLog.Debug("New client while old present, but no takeover")

				// First disconnect the old client and just track it
				h.disconnect(cOld)
				h.justTrack(cOld)

				// Next, send leaver messages to all the clients
				h.leaver(cOld)
				h.num++

				// Then add the new client and start it going with an
				// empty queue
				h.connect(c, NewQueue())

				// Finally send joiner/welcome messages
				h.joiner(c)
				h.welcome(c)
				h.num++

			case msg.Intent == "Joiner" && h.otherJoined(msg.From) == nil:
				// New joiner
				c := msg.From
				caseLog := fLog.New("cid", c.ID, "cref", c.Ref)
				caseLog.Debug("New joiner")

				// Connect the new client
				h.connect(c, NewQueue())

				// Send joiner and welcome messages
				h.joiner(c)
				h.welcome(c)
				h.num++

			case msg.Intent == "LostConnection":
				// A client receiver has lost the connection
				c := msg.From
				fLog.Debug("Got lost connection", "cid", c.ID, "cref", c.Ref)
				h.disconnect(c)

			case msg.Intent == "Peer":
				// We have a peer message
				c := msg.From
				caseLog := fLog.New("cid", c.ID, "cref", c.Ref)
				caseLog.Debug("Got peer msg", "content", string(msg.Body))

				toCls := h.joinedExcluding(c)
				envP := &Envelope{
					From:   []string{c.ID},
					To:     ids(toCls),
					Num:    h.num,
					Time:   nowMs(),
					Intent: "Peer",
					Body:   msg.Body,
				}

				caseLog.Debug("Sending peer messages")
				for _, cl := range toCls {
					caseLog.Debug("Sending peer msg", "tocref", cl.Ref)
					h.send(cl, envP)
				}

				caseLog.Debug("Sending receipt")
				envR := &Envelope{
					From:   envP.From,
					To:     envP.To,
					Num:    envP.Num,
					Time:   envP.Time,
					Intent: "Receipt",
					Body:   envP.Body,
				}
				h.send(c, envR)

				// Set the next message num
				h.num++

			default:
				// Should never get here
				fLog.Error("Cannot handle message", "msg", msg)
			}
			h.buffer.Clean()
		}

	}
}

// now in milliseconds past the epock
func nowMs() int64 {
	return time.Now().UnixNano() / 1000000
}

// canFulfill says if we can send the next num the client is expecting
func (h *Hub) canFulfill(id string, num int) bool {
	return num < 0 || num == h.num || h.buffer.Available(id, num)
}

// Is a client known and connected?
func (h *Hub) connected(c *Client) bool {
	return h.clients[c] == CONNECTED
}

// Is a client known and disconnected, but may reconnect?
func (h *Hub) mayReconnect(c *Client) bool {
	return h.clients[c] == MAYRECONNECT
}

// Is a client still joined, as far as the other clients are concerned?
func (h *Hub) stillJoined(c *Client) bool {
	return h.clients[c] == CONNECTED || h.clients[c] == MAYRECONNECT
}

// Is a client being tracked?
func (h *Hub) tracked(c *Client) bool {
	return h.clients[c] > 0
}

// remove a client from the list of tracked clients. This like replace,
// but there's no new client, so the buffer is lost.
func (h *Hub) remove(c *Client) {
	aLog.Debug("Removing client", "fn", "hub.remove",
		"cid", c.ID, "cref", c.Ref)
	delete(h.clients, c)
	h.buffer.Remove(c.ID)
}

// connect a client and start it going with a given queue.
func (h *Hub) connect(c *Client, q *Queue) {
	aLog.Debug("Connecting client", "fn", "hub.connect",
		"cid", c.ID, "cref", c.Ref)
	h.clients[c] = CONNECTED
	c.InitialQueue <- q
}

// disconnect a given client, although it (or, more correctly, another
// with the same ID) may reconnect.
func (h *Hub) disconnect(c *Client) {
	aLog.Debug("Disconnecting client", "fn", "hub.disconnect",
		"cid", c.ID, "cref", c.Ref)
	// Only do this if the client is connected, otherwise we may
	// close a channel a second time, or revive a just-tracking client.
	if h.connected(c) {
		close(c.Pending)
		h.clients[c] = MAYRECONNECT
	}
}

// justTrack a client, ready for when the superhub times it out;
// other clients now won't know about it.
func (h *Hub) justTrack(c *Client) {
	aLog.Debug("Just tracking client", "fn", "hub.justTrack",
		"cid", c.ID, "cref", c.Ref)
	if h.connected(c) {
		close(c.Pending)
	}
	h.clients[c] = TRACKEDONLY
}

// replace has a new (connected) client replacing an old joined one.
// The old one is shut down if it's still connected and we just track it.
// The new client is started off with the given queue.
func (h *Hub) replace(cNew *Client, qNew *Queue, cOld *Client) {
	fLog := aLog.New("fn", "hub.replace", "cnewref", cNew.Ref,
		"coldref", cOld.Ref)
	fLog.Debug("Replacing client")
	if !h.tracked(cOld) {
		fLog.Error("Old client not known")
		return
	}
	if h.connected(cOld) {
		fLog.Debug("Closing old channel")
		close(cOld.Pending)
	}
	h.clients[cOld] = TRACKEDONLY
	h.clients[cNew] = CONNECTED
	cNew.InitialQueue <- qNew
}

// welcome sends a Welcome message to just this client.
func (h *Hub) welcome(c *Client) {
	aLog.Debug("Sending welcome", "fn", "hub.welcome",
		"cid", c.ID, "cref", c.Ref)
	env := &Envelope{
		To:     []string{c.ID},
		From:   h.joinedIDsExcluding(c),
		Num:    h.num,
		Time:   nowMs(),
		Intent: "Welcome",
	}
	h.buffer.Add(c.ID, env)
	c.Pending <- env
}

// joiner sends a Joiner message to all clients (except c), about joiner c.
func (h *Hub) joiner(c *Client) {
	aLog.Debug("Sending joiner messages", "fn", "hub.joiner",
		"cid", c.ID, "cref", c.Ref)
	env := &Envelope{
		From:   []string{c.ID},
		To:     h.joinedIDsExcluding(c),
		Num:    h.num,
		Time:   nowMs(),
		Intent: "Joiner",
	}

	for _, cl := range h.allJoined() {
		if cl != c {
			h.send(cl, env)
		}
	}
}

// leaver message sent to all joined clients about leaver c.
func (h *Hub) leaver(c *Client) {
	aLog.Debug("Sending leaver messages", "fn", "hub.leaver",
		"cid", c.ID, "cref", c.Ref)
	env := &Envelope{
		From:   []string{c.ID},
		To:     h.allJoinedIDs(),
		Num:    h.num,
		Time:   nowMs(),
		Intent: "Leaver",
	}
	for _, cl := range h.allJoined() {
		h.send(cl, env)
	}
}

// send an envelope to a client (if it's connected) and buffer it (either way).
func (h *Hub) send(c *Client, env *Envelope) {
	h.buffer.Add(c.ID, env)
	if h.connected(c) {
		c.Pending <- env
	}
}

// allJoined finds all joined clients.
func (h *Hub) allJoined() []*Client {
	cOut := make([]*Client, 0)
	for c, _ := range h.clients {
		if h.stillJoined(c) {
			cOut = append(cOut, c)
		}
	}
	return cOut
}

// joinedExcluding finds all joined clients which aren't the given one.
// Matching is done on pointers.
func (h *Hub) joinedExcluding(cx *Client) []*Client {
	cOut := make([]*Client, 0)
	for c, _ := range h.clients {
		if c != cx && h.stillJoined(c) {
			cOut = append(cOut, c)
		}
	}
	return cOut
}

// joinedIDsExcluding finds the IDs of all joined clients which aren't
// the given client.
func (h *Hub) joinedIDsExcluding(cx *Client) []string {
	cOut := make([]string, 0)
	for c, _ := range h.clients {
		if c != cx && h.stillJoined(c) {
			cOut = append(cOut, c.ID)
		}
	}
	return cOut
}

// allJoinedIDs returns all the IDs known to the hub
func (h *Hub) allJoinedIDs() []string {
	out := make([]string, 0)
	for c, _ := range h.clients {
		if h.stillJoined(c) {
			out = append(out, c.ID)
		}
	}
	return out
}

// ids returns just the IDs of the given clients
func ids(cs []*Client) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

// otherJoined returns the other joined client with the same ID, or nil
func (h *Hub) otherJoined(c *Client) *Client {
	var cOther *Client
	for c2 := range h.clients {
		if c2.ID != c.ID || !h.stillJoined(c2) {
			continue
		}
		if cOther != nil {
			aLog.Error("Found a second client with the same ID",
				"fn", "hub.other", "cid", c.ID, "cref", c.Ref,
				"cotherref", cOther.Ref)
		}
		cOther = c2
	}
	return cOther
}
