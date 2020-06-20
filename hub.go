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
	// All clients known and their connection status.
	// True: Client is connected, and we will pass envelopes into it.
	// False: Client is disconnected, no running goroutines, but it's
	//     present as far as other clients are concerned because we might
	//     get a reconnection (before that times out). We will buffer
	//     envelopes for this client, even though we can't send them.
	// Only one client per ID should be known at any time.
	clients map[*Client]bool
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
		Timeout: make(chan *Client),
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
// them to the connected clients, buffers them for all known clients.
func (h *Hub) receiveInt() {
	fLog := aLog.New("fn", "hub.receiveInt")

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
			if !h.known(c) {
				caseLog.Debug("Client is not known; ignoring")
				continue
			}

			// We have a leaver
			caseLog.Debug("Timed-out client is a leaver; removing")
			h.remove(c)

			if len(h.clients) == 0 {
				caseLog.Debug("That was the last client; exiting")
				break readingLoop
			}

			// Send a leaver message to remaining clients
			h.leaver(c)
			h.num++
			caseLog.Debug("Sent leaver messages")

		case msg := <-h.Pending:
			fLog.Debug("Received pending message")

			switch {
			case msg.Env.Intent == "Joiner" &&
				!h.canFulfill(msg.From.ID, msg.From.Num):
				// New client but bad lastnum; eject client
				c := msg.From
				caseLog := fLog.New("cid", c.ID, "cref", c.Ref)
				caseLog.Debug("New client but bad num", "num", msg.From.Num)
				// Start the client sending messages, but shut it down
				// immediately, without it ever joining our known client list
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
				caseLog := fLog.New("cid", c.ID, "cref", c.Ref)
				cOld := h.other(msg.From)
				caseLog.Debug("New client taking over", "oldcref", cOld.Ref)

				// Give the new client its initial queue to start it off

				// Let the new client replace the old client and start it off
				h.replace(c, h.buffer.Queue(c.ID, c.Num), cOld)

			case msg.Env.Intent == "Joiner" &&
				h.other(msg.From) != nil &&
				msg.From.Num < 0:
				// New client for old ID, but didn't ask to take over
				c := msg.From
				cOld := h.other(msg.From)
				caseLog := fLog.New("newcid", c.ID, "newcref", c.Ref,
					"oldcref", cOld.Ref)
				caseLog.Debug("New client while old present, but no takeover")

				// First remove the old client
				h.remove(cOld)

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

			case msg.Env.Intent == "Joiner" && h.other(msg.From) == nil:
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

			case msg.Env.Intent == "LostConnection":
				// A client receiver has lost the connection
				c := msg.From
				fLog.Debug("Got lost connection", "cid", c.ID, "cref", c.Ref)
				h.disconnect(c)

			case msg.Env.Intent == "Peer":
				// We have a peer message
				c := msg.From
				caseLog := fLog.New("cid", c.ID, "cref", c.Ref)
				caseLog.Debug("Got peer msg", "content", string(msg.Env.Body))

				toCls := h.exclude(c)
				now := time.Now().Unix()
				msgP := &Message{
					From: msg.From,
					Env: &Envelope{
						From:   []string{c.ID},
						To:     ids(toCls),
						Num:    h.num,
						Time:   now,
						Intent: "Peer",
						Body:   msg.Env.Body,
					},
				}

				caseLog.Debug("Sending peer messages")
				for _, cl := range toCls {
					caseLog.Debug("Sending peer msg", "tocref", cl.Ref)
					h.send(cl, msgP)
				}

				caseLog.Debug("Sending receipt")
				msgR := &Message{
					From: c,
					Env: &Envelope{
						From:   msgP.Env.From,
						To:     msgP.Env.To,
						Num:    msgP.Env.Num,
						Time:   msgP.Env.Time,
						Intent: "Receipt",
						Body:   msgP.Env.Body,
					},
				}
				h.send(c, msgR)

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

// canFullfill says if we can send the next num the client is expecting
func (h *Hub) canFulfill(id string, num int) bool {
	pr := "["
	for _, e := range h.buffer.buf[id] {
		pr = pr + niceEnv(e)
	}
	pr = pr + "]"
	aLog.Debug("canFulfill", "id", id, "num", num, "buf", pr,
		"result", num < 0 || num == h.num || h.buffer.Available(id, num))
	return num < 0 || num == h.num || h.buffer.Available(id, num)
}

// Is a client known and connected?
func (h *Hub) connected(c *Client) bool {
	return h.clients[c]
}

// Is a client known and disconnected?
func (h *Hub) disconnected(c *Client) bool {
	conn, ok := h.clients[c]
	return ok && !conn
}

// Is a client known?
func (h *Hub) known(c *Client) bool {
	_, ok := h.clients[c]
	return ok
}

// remove a client from the list of known clients. This like replace,
// but there's no new client, so the buffer is lost.
func (h *Hub) remove(c *Client) {
	aLog.Debug("Removing client", "fn", "hub.remove",
		"cid", c.ID, "cref", c.Ref)
	delete(h.clients, c)
	h.buffer.Remove(c.ID)
}

// connect a client and start it going with a given queue
func (h *Hub) connect(c *Client, q *Queue) {
	aLog.Debug("Connecting client", "fn", "hub.connect",
		"cid", c.ID, "cref", c.Ref)
	h.clients[c] = true
	c.InitialQueue <- q
}

// disconnect a given client
func (h *Hub) disconnect(c *Client) {
	aLog.Debug("Disconnecting client", "fn", "hub.connect",
		"cid", c.ID, "cref", c.Ref)
	if h.connected(c) {
		close(c.Pending)
	}
	h.clients[c] = false
}

// Replace has a new (connected) client replacing an old one.
// The old one is shut down if it's still connected and forgotten about.
// The new client is started off with the given queue.
func (h *Hub) replace(cNew *Client, qNew *Queue, cOld *Client) {
	fLog := aLog.New("fn", "hub.replace", "cnewref", cNew.Ref,
		"coldref", cOld.Ref)
	fLog.Debug("Replacing client")
	if !h.known(cOld) {
		fLog.Warn("Old client not known")
		return
	}
	if h.connected(cOld) {
		fLog.Debug("Closing old channel")
		close(cOld.Pending)
	}
	delete(h.clients, cOld)
	h.clients[cNew] = true
	cNew.InitialQueue <- qNew
}

// welcome sends a Welcome message to just this client.
func (h *Hub) welcome(c *Client) {
	aLog.Debug("Sending welcome", "fn", "hub.welcome",
		"cid", c.ID, "cref", c.Ref)
	msg := &Message{
		From:  c,
		MType: websocket.BinaryMessage,
		Env: &Envelope{
			To:     []string{c.ID},
			From:   h.excludeID(c),
			Num:    h.num,
			Time:   time.Now().Unix(),
			Intent: "Welcome",
		},
	}
	h.buffer.Add(c.ID, msg.Env)
	c.Pending <- msg
}

// joiner sends a Joiner message to all clients (except c), about joiner c.
func (h *Hub) joiner(c *Client) {
	aLog.Debug("Sending joiner messages", "fn", "hub.joiner",
		"cid", c.ID, "cref", c.Ref)
	msg := &Message{
		From:  c,
		MType: websocket.BinaryMessage,
		Env: &Envelope{
			From:   []string{c.ID},
			To:     h.excludeID(c),
			Num:    h.num,
			Time:   time.Now().Unix(),
			Intent: "Joiner",
		},
	}

	for cl, _ := range h.clients {
		if cl != c {
			h.send(cl, msg)
		}
	}
}

// leaver message sent to all clients about leaver c.
func (h *Hub) leaver(c *Client) {
	aLog.Debug("Sending leaver messages", "fn", "hub.leaver",
		"cid", c.ID, "cref", c.Ref)
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
		h.send(cl, msg)
	}
}

// send an envelope to a client (if it's connected) and buffer it (either way).
func (h *Hub) send(c *Client, msg *Message) {
	h.buffer.Add(c.ID, msg.Env)
	if h.connected(c) {
		c.Pending <- msg
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

// excludeID finds the IDs of all clients which aren't the given client.
func (h *Hub) excludeID(cx *Client) []string {
	aLog.Debug("hub.excludeIDs, entering")
	cOut := make([]string, 0)
	for c, _ := range h.clients {
		if c != cx {
			cOut = append(cOut, c.ID)
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

// otherIDs returns the other client with the same ID, or nil
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
