// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

// How often we send pings
var pingFreq = 60 * time.Second

// How long we time out waiting for a pong or other data. Must be more
// than pingFreq.
var pongTimeout = (pingFreq * 5) / 4

// How long to allow to write to the websocket.
var writeTimeout = 10 * time.Second

// How long to allow for a reconnection if we lose the client
var reconnectionTimeout = 5 * time.Second

func init() {
	// Let's not generate near-identical client IDs on every restart
	rand.Seed(time.Now().UnixNano())
}

type Client struct {
	ID string
	// Envelope number expected when starting, or -1
	Num int
	// Ref for tracing purposes only
	Ref string
	// Don't close the websocket directly. That's managed internally.
	WS  *websocket.Conn
	Hub *Hub
	// Queue of older messages
	queue *Queue
	// Channel to receive the initial queue
	InitialQueue chan *PossibleQueue
	// To receive a message from the hub. The hub will close the channel
	// to indicate the client should disconnect and shut down.
	Pending chan *Envelope
	// pinger ticks for pinging
	pinger *time.Ticker
}

type PossibleQueue struct {
	queue *Queue
	err   error
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// If set, the Origin host is in r.Header["Origin"][0])
		// The request host is in r.Host
		// We won't worry about the origin, to help with testing locally
		return true
	},
}

// newClientID generates a random clientID string
func newClientID() string {
	return fmt.Sprintf(
		"%d.%d",
		time.Now().Unix(),
		rand.Int31(),
	)
}

// ClientIDOrNew returns the value of the client ID from the URL query,
// or a new ID if there's none there.
func ClientIDOrNew(query string) string {
	v, err := url.ParseQuery(query)
	if err != nil {
		aLog.Warn("Couldn't parse query string", "query", query)
		return newClientID()
	}
	gotID := v.Get("id")
	if gotID == "" {
		return newClientID()
	}
	return gotID
}

// Start attaches the client to its hub, allows the hub to check it has
// received a good request, and starts its goroutines if so.
func (c *Client) Start(w http.ResponseWriter, r *http.Request) {
	fLog := aLog.New("fn", "client.Start", "id", c.ID, "c", c.Ref)

	// First send a joiner message to the hub
	c.Hub.Pending <- &Message{
		From:   c,
		Intent: "Joiner",
	}

	// Wait for the initial queue, or an error if it's a bad request.
	// The error can only be a bad lastnum.
	init := <-c.InitialQueue
	if init.err != nil {
		aLog.Debug("Error instead of initial queue", "error", init.err)
		http.Error(w, init.err.Error(), http.StatusGone)
		Shub.Release(c.Hub, c)
		return
	}
	c.queue = init.queue

	// It's a good request, we can try to upgrade to a websocket
	ws, err := upgrader.Upgrade(w, r, make(http.Header))
	if err != nil {
		aLog.Warn("Upgrade error", "error", err)
		Shub.Release(c.Hub, c)
		return
	}
	c.WS = ws
	aLog.Info("Connected client", "id", c.ID, "num", c.Num, "ref", c.Ref)

	// Immediate termination for an excessive message
	c.WS.SetReadLimit(60 * 1024)

	// Set up pinging
	c.pinger = time.NewTicker(pingFreq)
	c.WS.SetReadDeadline(time.Now().Add(pongTimeout))
	c.WS.SetPongHandler(func(string) error {
		fLog.Debug("Start.SetPongHandler: Received pong")
		c.WS.SetReadDeadline(time.Now().Add(pongTimeout))
		return nil
	})

	// Start sending messages externally
	fLog.Debug("Adding for sendExt")
	WG.Add(1)
	go c.sendExt()

	// Start receiving messages from the outside
	fLog.Debug("Adding for receiveExt")
	WG.Add(1)
	go c.receiveExt()
}

// receiveExt is a goroutine that acts on external messages coming in.
func (c *Client) receiveExt() {
	fLog := aLog.New("fn", "client.receiveExt", "id", c.ID, "c", c.Ref)
	fLog.Debug("Entering")

	defer fLog.Debug("Done")
	defer WG.Done()

	// Read messages until we can no more
	for {
		fLog.Debug("Reading")
		_, msg, err := c.WS.ReadMessage()
		if err != nil {
			fLog.Debug("Read error", "error", err)
			break
		}
		// Currently just passes on the message type
		fLog.Debug("Read is good", "content", string(msg))
		c.Hub.Pending <- &Message{
			From:   c,
			Intent: "Peer",
			Body:   msg,
		}
	}

	// We've done reading, so announce a lost connection and set up a
	// signal for allowing a reconnection.

	fLog.Debug("Closing conn")
	c.WS.Close()
	c.Hub.Pending <- &Message{
		From:   c,
		Intent: "LostConnection",
	}
}

// sendExt is a goroutine that sends network messages out. These are
// pings and messages that have come from the hub. It will stop
// if its channel is closed or it can no longer write to the network.
func (c *Client) sendExt() {
	fLog := aLog.New("fn", "client.sendExt", "id", c.ID, "c", c.Ref)
	fLog.Debug("Entering")

	defer fLog.Debug("Goroutine done")
	defer WG.Done()

	// Go through scenarios until we need to shut down this client
	connected := true
	if !c.queue.Empty() {
		fLog.Debug("Scenario: connected, queued envelopes")
		connected = c.connectedWithQueued()
	}
	if connected && c.queue.Empty() {
		fLog.Debug("Scenario: connected, no queued envelopes")
		c.connectedNoneQueued()
	}

	// We are here due to either the channel being closed or the
	// network connection being closed. We need to make sure both are
	// true before continuing the shut down.
	fLog.Debug("Closing connection")
	c.WS.Close()
	aLog.Info("Closed connection", "id", c.ID)
	c.pinger.Stop()
	fLog.Debug("Waiting for channel close")
	for {
		if _, ok := <-c.Pending; !ok {
			break
		}
	}

	// We're done. Tell the superhub we're done with the hub
	fLog.Debug("Releasing hub")
	Shub.Release(c.Hub, c)
}

// connectedWithQueued is for processing messages from the hub while
// sending messages from the queue. Returns connected flag.
func (c *Client) connectedWithQueued() bool {
	fLog := aLog.New("fn", "client.connectedWithQueued",
		"id", c.ID, "ref", c.Ref)

	// Keep receiving internal messages
	for {
		fLog.Debug("Entering select")
		select {
		case env, ok := <-c.Pending:
			fLog.Debug("Got pending")
			if !ok {
				// Channel closed, we need to shut down
				fLog.Debug("Channel closed")
				return false
			}
			if env.Intent == "BadLastnum" {
				// This message is for us
				fLog.Debug("Got BadLastnum intent")
				c.closeWith("Bad lastnum")
				return false
			}
			// Message needs to go onto the queue
			fLog.Debug("Adding to queue", "env", niceEnv(env))
			c.queue.Add(env)

		case <-c.pinger.C:
			fLog.Debug("Sending ping")
			if err := c.WS.SetWriteDeadline(
				time.Now().Add(writeTimeout)); err != nil {
				// Write error, move to disconnected state
				fLog.Debug("Ping deadline error", "err", err)
				return false
			}
			if err := c.WS.WriteMessage(
				websocket.PingMessage, nil); err != nil {
				// Ping write error, move to disconnected state
				fLog.Debug("Ping write error", "err", err)
				return false
			}
		default:
			fLog.Debug("Sending envelope from queue")
			env, err := c.queue.Get()
			if err != nil {
				fLog.Debug("Problem getting envelope", "err", err.Error())
				return false
			}
			fLog.Debug("Got queued envelope okay", "env", niceEnv(env))
			if err := c.WS.SetWriteDeadline(
				time.Now().Add(writeTimeout)); err != nil {
				// Write error, move to disconnected state
				fLog.Debug("Message deadline error", "err", err)
				return false
			}
			if err := c.WS.WriteJSON(env); err != nil {
				// Write error, move to disconnected state
				fLog.Debug("Message write error", "err", err)
				return false
			}
			// Send was okay
			fLog.Debug("Sent okay")
			if c.queue.Empty() {
				fLog.Debug("Queue is empty; reselecting scenario")
				return true
			}
		}
	}
}

// connectedNoneQueued is for processing messages from the hub when
// the queue is empty. Returns when we're disconnected.
func (c *Client) connectedNoneQueued() {
	fLog := aLog.New("fn", "client.connectedNoneQueued", "id", c.ID, "c", c.Ref)

	// Keep receiving internal messages
	for {
		fLog.Debug("Entering select")
		select {
		case env, ok := <-c.Pending:
			fLog.Debug("Got pending")
			if !ok {
				// Channel closed, we need to shut down
				fLog.Debug("Channel closed")
				return
			}
			if env.Intent == "BadLastnum" {
				// This message is for us
				fLog.Debug("Got BadLastnum intent")
				c.closeWith("Bad lastnum")
				return
			}
			// We should send this message
			fLog.Debug("Got envelope", "env", niceEnv(env))
			if err := c.WS.SetWriteDeadline(
				time.Now().Add(writeTimeout)); err != nil {
				// Write error, move to disconnected state
				fLog.Debug("Deadline error", "err", err)
				return
			}
			if err := c.WS.WriteJSON(env); err != nil {
				// Write error, move to disconnected state
				fLog.Debug("WriteJSON error", "err", err)
				return
			}
			fLog.Debug("Wrote JSON", "env", niceEnv(env))
		case <-c.pinger.C:
			fLog.Debug("Sending ping")
			if err := c.WS.SetWriteDeadline(
				time.Now().Add(writeTimeout)); err != nil {
				// Write error, move to disconnected state
				fLog.Debug("Deadline2 error", "err", err)
				return
			}
			if err := c.WS.WriteMessage(
				websocket.PingMessage, nil); err != nil {
				// Ping write error, move to disconnected state
				fLog.Debug("Write2 error", "err", err)
				return
			}
		}
	}
}

// closeWith closes the connection with the given error message
func (c *Client) closeWith(desc string) {
	c.WS.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, desc),
		time.Now().Add(writeTimeout),
	)
	c.WS.Close()
}

func niceEnv(e *Envelope) string {
	return fmt.Sprintf("Env{Num:%d,Intent:%s,Body:%s}",
		e.Num, e.Intent, string(e.Body))
}
