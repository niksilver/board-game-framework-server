// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/inconshreveable/log15"
)

// tConn is a websocket.Conn whose ReadMessage can time out safely
type tConn struct {
	ws       *websocket.Conn
	readRes  chan readRes
	id       string
	chReadMx sync.Mutex // Ensure only one func reads from readRes at a time
}

// A combined ReadMessage result that can be put into a channel.
type readRes struct {
	mType int
	msg   []byte
	err   error
}

// tLog is a logger for our tests only.
var tLog = log15.New("side", "test")

// uLog is a logger for our these utils only.
var uLog = log15.New("side", "utils")

func init() {
	tLog.SetHandler(
		log15.LvlFilterHandler(
			// log15.LvlWarn,
			log15.LvlDebug,
			// log15.DiscardHandler(),
			FlushingStdoutHandler{},
		))
	uLog.SetHandler(
		log15.LvlFilterHandler(
			// log15.LvlWarn,
			log15.LvlDebug,
			log15.DiscardHandler(),
			// log15.StdoutHandler,
		))
}

// sameElements tests if two string slices have the same elements
// (including the same duplicates), regardless of order.
func sameElements(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := make([]string, len(a))
	bs := make([]string, len(b))
	copy(as, a)
	copy(bs, b)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// newTestServer creates a new server to connect to, using the given handler.
func newTestServer(hdlr http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(hdlr))
}

// dial connects to a test server, sending a clientID (if non-empty)
// and last num received (if non-negative).
func dial(serv *httptest.Server, path string, clientID string, num int) (
	ws *websocket.Conn,
	resp *http.Response,
	err error,
) {
	// Convert http://a.b.c.d to ws://a.b.c.d
	// and add the given path
	url := "ws" + strings.TrimPrefix(serv.URL, "http") + path

	// Add a last num received, if we've got one
	if num >= 0 {
		url = url + "?lastnum=" + strconv.Itoa(num)
	}

	// If necessary, creater a header with the given cookie
	var header http.Header
	if clientID != "" {
		header = cookieRequestHeader("clientID", clientID)
	}

	// Connect to the server

	return websocket.DefaultDialer.Dial(url, header)
}

// cookieRequestHeader returns a new http.Header for a client request,
// in which only a single cookie is sent, with some value.
func cookieRequestHeader(name string, value string) http.Header {
	cookie := &http.Cookie{
		Name:  name,
		Value: value,
	}
	cookieStr := cookie.String()
	header := http.Header(make(map[string][]string))
	header.Add("Cookie", cookieStr)

	return header
}

// newTConn creates a new timeoutable connection from the given one.
func newTConn(ws *websocket.Conn, id string) *tConn {
	return &tConn{
		ws: ws,
		// If this is not nil it means a readMessage call is in flight
		readRes:  nil,
		id:       id,
		chReadMx: sync.Mutex{},
	}
}

// readMessage is like websocket.ReadMessage, but it can time out safely
// using the timeout given, in milliseconds. It returns the result of the
// read and false (no timeout), or a zero value and true (timed out).
// The result of the read may still include a read error.
// If using a `tConn` to read and if it times out, then the next
// read should also be using the `tConn`, not the usual `websocket.Conn`,
// and it should be closed using `tConn.close()`. This is
// because behind the scenes the read operation will still be in progress,
// and needs to be reused or tidied up.
func (c *tConn) readMessage(timeout int) (readRes, bool) {
	if c.readRes == nil {
		// We're not already running a read, so let's start one
		c.readRes = make(chan readRes)
		WG.Add(1)
		uLog.Debug("tConn.readMessage, entering goroutine", "id", c.id)
		go func() {
			defer uLog.Debug("tConn.readMessage, exited goroutine", "id", c.id)
			defer WG.Done()
			uLog.Debug("tConn.readMessage, reading", "id", c.id)
			mType, msg, err := c.ws.ReadMessage()
			uLog.Debug("tConn.readMessage, sending result", "msg", string(msg), "error", err, "id", c.id)
			c.readRes <- readRes{mType, msg, err}
			uLog.Debug("tConn.readMessage, sent result", "id", c.id)
		}()
	}
	// Now wait for a result or a timeout
	timeoutC := time.After(time.Duration(timeout) * time.Millisecond)
	c.chReadMx.Lock()
	defer c.chReadMx.Unlock()
	select {
	case rr := <-c.readRes:
		// We've got a result from the readMessage operation
		c.readRes = nil
		return rr, false
	case <-timeoutC:
		// We timed out
		return readRes{}, true
	}
}

// readEnvelope expects to read (and will return) an envelope. It will
// return an error if it times out (timeout is in milliseconds) or some
// other error.
// trace is a format string to put in an error message to aid tracing
// (may be empty) plus optional values.
func (c *tConn) readEnvelope(timeout int, trace string, a ...interface{}) (*Envelope, error) {
	if trace != "" {
		trace = " (" + fmt.Sprintf(trace, a...) + ")"
	}

	rr, timedOut := c.readMessage(500)
	if timedOut {
		return nil, fmt.Errorf("Timed out%s", trace)
	}
	if rr.err != nil {
		return nil, fmt.Errorf("Read error%s: %s", trace, rr.err.Error())
	}
	env := Envelope{}
	err := json.Unmarshal(rr.msg, &env)
	if err != nil {
		return nil, fmt.Errorf("Unmarshalling error%s: %s", trace, err.Error())
	}
	return &env, nil
}

// close the `tConn`. Always use this to close the connection, instead
// of the `Conn.Close()`.
func (c *tConn) close() {
	uLog.Debug("tConn.close, entering", "id", c.id)
	uLog.Debug("tConn.close, closing conn", "id", c.id)
	c.ws.Close()
	// If tConn.readMessage is running we want to ensure sending to
	// c.readRes doesn't block.
	// We want to ensure readMessage won't block while sending to c.readRes.
	// That won't happen if c.readRes is nil.
	// But if c.readRes isn't nil it's because either
	// (i) readMessage is going to consume it and pass it back, or
	// (ii) it's sitting there waiting for the next readMessage call.
	// In the first case readMessage will have the lock, and release the
	// lock only after consuming it.
	// In the second case we can grab the lock then consume it.
	if c.readRes != nil {
		uLog.Debug("tConn.close, locking channel read", "id", c.id)
		c.chReadMx.Lock()
		// Now either the message has been consumed and the channel is nil,
		// or there is a message waiting to be consumed.
		if c.readRes != nil {
			uLog.Debug("tConn.close, consuming from channel", "id", c.id)
			<-c.readRes
			uLog.Debug("tConn.close, consumed from channel", "id", c.id)
			c.readRes = nil
		}
		uLog.Debug("tConn.close, unlocking channel read", "id", c.id)
		c.chReadMx.Unlock()
	}
	uLog.Debug("tConn.close, exiting", "id", c.id)
}

// swallow expects the next message to be of the given intent.
// It returns an error if not, or if it gets an error.
// It will only wait 500 ms to read any message.
// If there's an error, then future reads must be from the `tConn`,
// not the `websocket.Conn`, because a "timed out" error will mean there
// is still a read operation pending, and the `tConn` can handle that.
func (ws *tConn) swallow(intent string) error {
	var env Envelope
	rr, timedOut := ws.readMessage(500)
	if timedOut {
		return fmt.Errorf("readMessage timed out")
	}
	if rr.err != nil {
		return rr.err
	}
	err := json.Unmarshal(rr.msg, &env)
	if err != nil {
		return err
	}
	if env.Intent != intent {
		return fmt.Errorf(
			"Expected intent '%s' but got '%s'", intent, env.Intent,
		)
	}
	return nil
}

// intentExp is an expectation of some intent received from a `tConn`
// websocket connection.
type intentExp struct {
	desc   string
	ws     *tConn
	intent string
}

// swallowMany expects a series of messages to be of the given intents,
// in the order given. It retuns an error as soon as one of them is not.
// It will wait only 500 ms to read any message.
// If there's an error, then future reads must be from the relevant `tConn`,
// not the `websocket.Conn`, because a "timed out" error will mean there
// is still a read operation pending, and the `tConn` can handle that.
func swallowMany(exps ...intentExp) error {
	for _, exp := range exps {
		err := exp.ws.swallow(exp.intent)
		if err != nil {
			return fmt.Errorf("%s: %s", exp.desc, err.Error())
		}
	}
	return nil
}

// readPeerMessage is like websocket's ReadMessage, but if it successfully
// reads a message whose intent is not "Peer" it will try again. If it
// gets an error, it will return that. It will only wait
//`timeout` milliseconds to read any message.
// If there's an error, then future reads must be from the `tConn`,
// not the `websocket.Conn`, because a "timed out" error will mean there
// is still a read operation pending, and the `tConn` can handle that.
func (ws *tConn) readPeerMessage(timeout int) (int, []byte, error) {
	var env Envelope
	for {
		rr, timedOut := ws.readMessage(timeout)
		if timedOut {
			return 0, []byte{}, fmt.Errorf("readMessage timed out")
		}
		if rr.err != nil {
			return rr.mType, rr.msg, rr.err
		}
		err := json.Unmarshal(rr.msg, &env)
		if err != nil {
			return 0, []byte{}, err
		}
		if env.Intent == "Peer" {
			return rr.mType, rr.msg, nil
		}
	}
}

// expectNoMessage expects no message within a timeout period (milliseconds).
// If it gets one it returns an error.
// If this function returns nil, then future reads must be from the `tConn`,
// not the `websocket.Conn`, because that means the read timed out, so there
// is still a read operation pending, and the `tConn` can handle that.
func (ws *tConn) expectNoMessage(timeout int) error {
	rr, timedOut := ws.readMessage(timeout)
	if timedOut {
		return nil
	}
	if rr.err != nil {
		return fmt.Errorf("Got non-timeout error: %s", rr.err.Error())
	}
	return fmt.Errorf("Wrongly got message '%s'", string(rr.msg))
}

// Compute the max length of two slices
func maxLength(a []string, b []string) int {
	if len(a) > len(b) {
		return len(a)
	}
	return len(b)
}

// A simple semaphore which starts primed and can be marked done.

type semaphore struct {
	closed bool
	cloMux sync.Mutex
	waiter chan bool
}

func newSemaphore() *semaphore {
	return &semaphore{
		closed: false,
		cloMux: sync.Mutex{},
		waiter: make(chan bool),
	}
}

func (s *semaphore) done() {
	defer s.cloMux.Unlock()
	s.cloMux.Lock()

	if !s.closed {
		s.closed = true
		close(s.waiter)
	}
}

func (s *semaphore) isDone() bool {
	defer s.cloMux.Unlock()
	s.cloMux.Lock()
	return s.closed
}

func (s *semaphore) wait() {
	<-s.waiter
}
