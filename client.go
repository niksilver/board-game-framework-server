// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"sync"
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
var reconnectionTimeout = 3 * time.Second

func init() {
	// Let's not generate near-identical client IDs on every restart
	rand.Seed(time.Now().UnixNano())
}

type Client struct {
	ID string
	// For tracing purposes only
	Ref string
	// Don't close the websocket directly. That's managed internally.
	WS  *websocket.Conn
	Hub *Hub
	// To receive internal message from the hub. The hub will close it
	// once it knows the client wants to stop.
	Pending chan *Message
	// To receive a queue of messages when picking up from a
	// disconnected client with the same ID.
	QueueC chan []*Message
	queue  []*Message
	qMux   sync.Mutex
	// pinger ticks for pinging
	pinger *time.Ticker
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// If set, the Origin host is in r.Header["Origin"][0])
		// The request host is in r.Host
		// We won't worry about the origin, to help with testing locally
		return true
	},
}

// Upgrade converts an http request to a websocket, ensuring the client ID
// is sent. The ID should be set properly before entering.
func Upgrade(
	w http.ResponseWriter,
	r *http.Request,
	clientID string,
) (*websocket.Conn, error) {
	maxAge := 60 * 60 * 24 * 365 * 100 // 100 years, default expiration
	if clientID == "" {
		// Annul the cookie
		maxAge = -1
	}
	cookie := &http.Cookie{
		Name:   "clientID",
		Value:  clientID,
		Path:   "/",
		MaxAge: maxAge,
	}
	cookieStr := cookie.String()
	header := http.Header(make(map[string][]string))
	header.Add("Set-Cookie", cookieStr)

	return upgrader.Upgrade(w, r, header)
}

// NewClientID generates a random clientID string
func NewClientID() string {
	return fmt.Sprintf(
		"%d.%d",
		time.Now().Unix(),
		rand.Int31(),
	)
}

// clientID returns the value of the clientID cookie, or empty string
// if there's none there.
func ClientID(cookies []*http.Cookie) string {
	for _, cookie := range cookies {
		if cookie.Name == "clientID" {
			return cookie.Value
		}
	}

	return ""
}

// ClientIDOrNew returns the value of the clientID cookie, or a new ID
// if there's none there.
func ClientIDOrNew(cookies []*http.Cookie) string {
	clientID := ClientID(cookies)
	if clientID == "" {
		return NewClientID()
	}
	return clientID
}

// clientID returns the Max-Age value of the clientID cookie,
// or 0 if there's none there
func ClientIDMaxAge(cookies []*http.Cookie) int {
	for _, cookie := range cookies {
		if cookie.Name == "clientID" {
			return cookie.MaxAge
		}
	}

	return 0
}

// Start attaches the client to its hub and starts its goroutines.
func (c *Client) Start() {
	fLog := aLog.New("fn", "client.Start", "id", c.ID, "c", c.Ref)

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
	fLog.Debug("client.start, adding for sendExt")
	WG.Add(1)
	go c.sendExt()

	// Start receiving messages from the outside
	fLog.Debug("client.start, adding for receiveExt")
	WG.Add(1)
	go c.receiveExt()
}

// receiveExt is a goroutine that acts on external messages coming in.
func (c *Client) receiveExt() {
	fLog := aLog.New("fn", "client.receiveExt", "id", c.ID, "c", c.Ref)
	fLog.Debug("Entering")

	defer fLog.Debug("Done")
	defer WG.Done()

	// First send a joiner message
	c.Hub.Pending <- &Message{
		From: c,
		Env: &Envelope{
			Intent: "Joiner",
		},
	}

	// Read messages until we can no more
	for {
		fLog.Debug("Reading")
		mType, msg, err := c.WS.ReadMessage()
		if err != nil {
			fLog.Debug("Read error", "error", err)
			break
		}
		// Currently just passes on the message type
		fLog.Debug("Read is good")
		c.Hub.Pending <- &Message{
			From:  c,
			MType: mType,
			Env:   &Envelope{Body: msg},
		}
	}

	// We've done reading, so announce a lost connection and set up a
	// signal for allowing a reconnection.

	fLog.Debug("Closing conn")
	c.WS.Close()
	c.Hub.Pending <- &Message{
		From: c,
		Env: &Envelope{
			Intent: "LostConnection",
		},
	}

	time.Sleep(reconnectionTimeout)
	c.Hub.Pending <- &Message{
		From: c,
		Env: &Envelope{
			Intent: "ReconnectionTimeout",
		},
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

	// Keep reselecting scenarios until we need to exit
	connected := true
	exit := false
scenarios:
	for !exit {
		switch {
		case connected && c.queueEmpty():
			fLog.Debug("Scenario: connected, queue empty")
			connected, exit = c.connectedQueueEmpty()
		case connected && !c.queueEmpty():
			fLog.Debug("Scenario: connected, queue not empty")
			connected, exit = c.connectedQueueNotEmpty()
		case !connected:
			fLog.Debug("Scenario: disconnected")
			c.disconnected()
			break scenarios
		}
	}

	// We are here due to either the channel being closed or the
	// network connection being closed. We need to make sure both are
	// true before continuing the shut down.
	fLog.Debug("Closing conn")
	c.WS.Close()
	c.pinger.Stop()
	fLog.Debug("Waiting for channel close")
	for {
		if _, ok := <-c.Pending; !ok {
			break
		}
	}

	// We're done. Tell the superhub we're done with the hub
	fLog.Debug("Releasing hub")
	Shub.Release(c.Hub)
}

// connectedQueueEmpty is for processing messages from the hub when
// the queue is empty. Returns connected flag and exit flag.
func (c *Client) connectedQueueEmpty() (bool, bool) {
	fLog := aLog.New("fn", "client.connectedQueueEmpty", "id", c.ID, "c", c.Ref)

	// Keep receiving internal messages
	for {
		fLog.Debug("Entering select")
		select {
		case m, ok := <-c.Pending:
			fLog.Debug("Got pending")
			if !ok {
				// Channel closed, we need to shut down
				fLog.Debug("Channel closed")
				return true, true
			}
			if m.Env.Intent == "LostConnection" {
				fLog.Debug("Got LostConnection intent")
				return false, false
			}
			// We should send this message
			if err := c.WS.SetWriteDeadline(
				time.Now().Add(writeTimeout)); err != nil {
				// Write error, move to disconnected state
				fLog.Debug("Deadline1 error", "err", err)
				return false, false
			}
			if err := c.WS.WriteJSON(m.Env); err != nil {
				// Write error, move to disconnected state
				fLog.Debug("Write1 error", "err", err)
				return false, false
			}
		case q := <-c.QueueC:
			fLog.Debug("Got a queue")
			c.qMux.Lock()
			c.queue = q
			c.qMux.Unlock()
			fLog.Debug("Have queue, reselecting scenario")
			return true, false
		case <-c.pinger.C:
			fLog.Debug("Sending ping")
			if err := c.WS.SetWriteDeadline(
				time.Now().Add(writeTimeout)); err != nil {
				// Write error, move to disconnected state
				fLog.Debug("Deadline2 error", "err", err)
				return false, false
			}
			if err := c.WS.WriteMessage(
				websocket.PingMessage, nil); err != nil {
				// Ping write error, move to disconnected state
				fLog.Debug("Write2 error", "err", err)
				return false, false
			}
		}
	}
}

// connectedQueueNotEmpty is for processing messages from the hub while
// sending messages from the queue. Returns connected flag and exit flag.
func (c *Client) connectedQueueNotEmpty() (bool, bool) {
	fLog := aLog.New("fn", "client.connectedQueueNotEmpty", "id", c.ID, "c", c.Ref)

	// Keep receiving internal messages
	for {
		fLog.Debug("Entering select")
		select {
		case m, ok := <-c.Pending:
			fLog.Debug("Got pending")
			if !ok {
				// Channel closed, we need to shut down
				fLog.Debug("Channel closed")
				return true, true
			}
			if m.Env.Intent == "LostConnection" {
				// This message is for us
				fLog.Debug("Got LostConnection intent")
				return false, false
			}
			// Message needs to go onto the end of the queue
			c.qMux.Lock()
			c.queue = append(c.queue, m)
			c.qMux.Unlock()
		case <-c.QueueC:
			fLog.Debug("Got a queue while processing queue")
			panic("Should not have been sent a queue")
		case <-c.pinger.C:
			fLog.Debug("Sending ping")
			if err := c.WS.SetWriteDeadline(
				time.Now().Add(writeTimeout)); err != nil {
				// Write error, move to disconnected state
				fLog.Debug("Ping deadline error", "err", err)
				return false, false
			}
			if err := c.WS.WriteMessage(
				websocket.PingMessage, nil); err != nil {
				// Ping write error, move to disconnected state
				fLog.Debug("Ping write error", "err", err)
				return false, false
			}
		default:
			fLog.Debug("Sending message at head of queue")
			c.qMux.Lock()
			m := c.queue[0]
			if err := c.WS.SetWriteDeadline(
				time.Now().Add(writeTimeout)); err != nil {
				// Write error, move to disconnected state
				fLog.Debug("Message deadline error", "err", err)
				c.qMux.Unlock()
				return false, false
			}
			if err := c.WS.WriteJSON(m.Env); err != nil {
				// Write error, move to disconnected state
				fLog.Debug("Message write error", "err", err)
				c.qMux.Unlock()
				return false, false
			}
			// Send was okay
			c.queue = c.queue[:len(c.queue)]
			c.qMux.Unlock()
			if len(c.queue) == 0 {
				fLog.Debug("Queue now empty; reselecting scenario")
				return true, false
			}
		}
	}
}

// disconnected is for processing messages from the hub in the hope
// that we'll get a reconnection in time. Returns when the pending
// channel closes.
func (c *Client) disconnected() {
	fLog := aLog.New("fn", "client.disconnected", "id", c.ID, "c", c.Ref)

	// Keep receiving internal messages
	for {
		fLog.Debug("Entering select")
		select {
		case m, ok := <-c.Pending:
			fLog.Debug("Got pending")
			if !ok {
				// Channel closed, we need to shut down
				fLog.Debug("Channel closed")
				return
			}
			if m.Env.Intent == "LostConnection" {
				// This message is for us
				fLog.Debug("Got LostConnection while disconnected")
				continue
			}
			// Message needs to go onto the end of the queue
			c.qMux.Lock()
			c.queue = append(c.queue, m)
			c.qMux.Unlock()
		case <-c.QueueC:
			fLog.Debug("Got a queue while disconnected")
			panic("Got queue while disconnected")
		case <-c.pinger.C:
			fLog.Debug("Got a ping message; ignoring")
		}
	}
}

// getQueue will get a copy of the queue
func (c *Client) getQueue() []*Message {
	defer c.qMux.Unlock()
	c.qMux.Lock()

	qOut := make([]*Message, len(c.queue))
	copy(qOut, c.queue)
	return qOut
}

func (c *Client) queueEmpty() bool {
	defer c.qMux.Unlock()
	c.qMux.Lock()
	return len(c.queue) == 0
}
