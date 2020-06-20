// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Tests around sequencing

func TestHubSeq_BouncesToOtherClients(t *testing.T) {
	// Just for this test, lower the reconnectionTimeout so that a
	// Leaver message is triggered reasonably quickly.

	oldReconnectionTimeout := reconnectionTimeout
	reconnectionTimeout = 250 * time.Millisecond
	defer func() {
		reconnectionTimeout = oldReconnectionTimeout
	}()

	// Start a server
	serv := newTestServer(bounceHandler)
	defer serv.Close()

	// Connect 3 clients
	// We'll make sure all the clients have been added to hub, and force
	// the order by waiting on messages.

	game := "/hub.bounces.to.other"

	// We'll want to check From, To and Time fields, as well as
	// message contents.
	// Because we have 3 clients we'll have 2 listed in the To field.

	// Client 1 joins normally

	ws1, _, err := dial(serv, game, "CL1", -1)
	defer ws1.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws1 := newTConn(ws1, "CL1")
	if err := tws1.swallow("Welcome"); err != nil {
		t.Fatalf("Welcome error for ws1: %s", err)
	}

	// Client 2 joins, and client 1 gets a joiner message

	ws2, _, err := dial(serv, game, "CL2", -1)
	defer ws2.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws2 := newTConn(ws2, "CL2")
	if err = swallowMany(
		intentExp{"CL2 joining, ws2", tws2, "Welcome"},
		intentExp{"CL2 joining, ws1", tws1, "Joiner"},
	); err != nil {
		t.Fatal(err)
	}

	// Client 3 joins, and clients 1 and 2 get joiner messages.

	ws3, _, err := dial(serv, game, "CL3", -1)
	defer ws3.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws3 := newTConn(ws3, "CL3")
	if err = swallowMany(
		intentExp{"CL3 joining, ws2", tws3, "Welcome"},
		intentExp{"CL3 joining, ws1", tws1, "Joiner"},
		intentExp{"CL3 joining, ws2", tws2, "Joiner"},
	); err != nil {
		t.Fatal(err)
	}

	// Create 10 messages to send
	msgs := []string{
		"m0", "m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8", "m9",
	}

	// Send 10 messages from client 1

	for i := 0; i < 10; i++ {
		msg := []byte(msgs[i])
		if err := ws1.WriteMessage(websocket.BinaryMessage, msg); err != nil {
			t.Fatalf("Write error for message %d: %s", i, err)
		}
	}

	// We expect 10 receipts to client 1 and 10 messages to clients 2 and 3

	testMessage := func(tws *tConn, twsName string, intent string, i int) {
		rr, timedOut := tws.readMessage(500)
		if timedOut {
			t.Fatalf("Timed out reading %s, i=%d", twsName, i)
		}
		if rr.err != nil {
			t.Fatalf("Read error, %s, i=%d: %s", twsName, i, rr.err.Error())
		}
		env := Envelope{}
		err := json.Unmarshal(rr.msg, &env)
		if err != nil {
			t.Fatalf("Could not unmarshal '%s': %s", rr.msg, err.Error())
		}
		if string(env.Body) != string(msgs[i]) {
			t.Errorf("%s, i=%d, received '%s' but expected '%s'",
				twsName, i, env.Body, msgs[i],
			)
		}
		if env.Intent != intent {
			t.Errorf("%s, i=%d, got intent '%s' but expected '%s'",
				twsName, i, env.Intent, intent,
			)
		}
		if !sameElements(env.From, []string{"CL1"}) {
			t.Errorf("%s, i=%d, got From %v but expected [CL1]",
				twsName, i, env.From,
			)
		}
		if !sameElements(env.To, []string{"CL2", "CL3"}) {
			t.Errorf("%s, i=%d, got To %v but expected [CL2, CL3]",
				twsName, i, env.To,
			)
		}
	}

	for i := 0; i < 10; i++ {
		testMessage(tws1, "tws1", "Receipt", i)
		testMessage(tws2, "tws3", "Peer", i)
		testMessage(tws3, "tws3", "Peer", i)
	}

	// Tidy up and check everything in the main app finishes
	tLog.Debug("TestHubSeq_BouncesToOtherClients, closing off")
	tws1.close()
	tws2.close()
	tws3.close()
	tLog.Debug("TestHubSeq_BouncesToOtherClients, waiting on group")
	WG.Wait()
}

// A test for general connecting, disconnecting and message sending...
// This just needs to run and not deadlock.
func TestHubSeq_GeneralChaos(t *testing.T) {
	// Just for this test, lower the reconnectionTimeout so that a
	// Leaver message is triggered reasonably quickly.

	oldReconnectionTimeout := reconnectionTimeout
	reconnectionTimeout = 250 * time.Millisecond
	defer func() {
		reconnectionTimeout = oldReconnectionTimeout
	}()

	// Tracking our connections and clients
	cMap := make(map[string]*websocket.Conn)
	cSlice := make([]string, 0)
	consumed := 0

	// Start a web server
	serv := newTestServer(bounceHandler)
	defer serv.Close()

	// A client should consume messages until done
	w := sync.WaitGroup{}
	consume := func(ws *websocket.Conn, id string) {
		defer w.Done()
		for {
			_, _, err := ws.ReadMessage()
			if err == nil {
				consumed += 1
			} else {
				tLog.Debug("Chaos.consume, error reading", "id", id)
				break
			}
		}
		tLog.Debug("Chaos.consume, closing", "id", id)
		ws.Close()
		tLog.Debug("Chaos.consume, closed", "id", id)
	}

	for i := 0; i < 100; i++ {
		action := rand.Float32()
		cCount := len(cSlice)
		switch {
		case i < 10 || action < 0.25:
			// New client join
			id := "CHAOS" + strconv.Itoa(i)
			ws, _, err := dial(serv, "/hub.chaos", id, -1)
			defer func() {
				ws.Close()
			}()
			tLog.Debug("Chaos, adding", "id", id)
			if err != nil {
				t.Fatalf("Couldn't dial, i=%d, error '%s'", i, err.Error())
			}
			cMap[id] = ws
			cSlice = append(cSlice, id)
			w.Add(1)
			go consume(ws, id)
			tLog.Debug("Chaos, added", "id", id)
		case cCount > 0 && action >= 0.25 && action < 0.35:
			// Some client leaves
			idx := rand.Intn(len(cSlice))
			id := cSlice[idx]
			tLog.Debug("Chaos, leaving", "id", id)
			ws := cMap[id]
			ws.Close()
			delete(cMap, id)
			cSlice = append(cSlice[:idx], cSlice[idx+1:]...)
			tLog.Debug("Chaos, left", "id", id)
		case cCount > 0:
			// Some client sends a message
			idx := rand.Intn(len(cSlice))
			id := cSlice[idx]
			tLog.Debug("Chaos, sending", "id", id)
			ws := cMap[id]
			msg := "Message " + strconv.Itoa(i)
			err := ws.WriteMessage(websocket.BinaryMessage, []byte(msg))
			if err != nil {
				t.Fatalf(
					"Couldn't write message, i=%d, id=%s error '%s'",
					i, id, err.Error(),
				)
			}
			tLog.Debug("Chaos, sent", "id", id)
		default:
			// Can't take any action
		}
	}

	// Close remaining connections and wait for test goroutines
	for _, ws := range cMap {
		ws.Close()
	}
	w.Wait()

	// Check everything in the main app finishes
	tLog.Debug("TestHubSeq_GeneralChaos, waiting on group")
	WG.Wait()
}

func TestHubSeq_NonReadingClientsDontBlock(t *testing.T) {
	// We'll have 10 clients, of which only the first and
	// last are polite. The others will just not read anything
	max := 10
	twss := make([]*tConn, max)

	// Just for this test, lower the reconnectionTimeout so that a
	// Leaver message is triggered reasonably quickly.

	oldReconnectionTimeout := reconnectionTimeout
	reconnectionTimeout = 250 * time.Millisecond
	defer func() {
		reconnectionTimeout = oldReconnectionTimeout
	}()

	// Start a web server
	serv := newTestServer(bounceHandler)
	defer serv.Close()

	// A polite client should consume messages until done
	w := sync.WaitGroup{}
	consume := func(tws *tConn, id string) {
		defer w.Done()
		for {
			rr, timedOut := tws.readMessage(30000)
			if timedOut {
				break
			}
			if rr.err == nil {
				// Got a message
			} else {
				break
			}
		}
		tws.close()
	}

	// Let the clients join the game
	for i := 0; i < max; i++ {
		id := "BL" + strconv.Itoa(i)
		ws, _, err := dial(serv, "/hub.max", id, -1)
		tws := newTConn(ws, id)
		defer tws.close()
		if err != nil {
			t.Fatalf("Couldn't dial, i=%d, error '%s'", i, err.Error())
		}
		twss[i] = tws
		if i == 0 || i == max-1 {
			w.Add(1)
			go consume(tws, id)
		}
	}

	// Avoid blocking for any length of time. We'll time this all
	// out after 3 seconds.
	allDone := make(chan bool)
	timeOut := time.After(300 * time.Second)
	w.Add(1)
	go func() {
		defer w.Done()
		select {
		case <-allDone:
			// All is good
		case <-timeOut:
			// Timed out - exit
			t.Errorf("Timed out")
			for _, tws := range twss {
				tws.close()
			}
		}
	}()

	// Have the first and last clients send lots of messages
	for i := 0; i < 5000; i++ {
		msg := []byte("BLOCK-MSG-" + strconv.Itoa(i))
		if err := twss[0].ws.WriteMessage(
			websocket.BinaryMessage, msg); err != nil {
			t.Fatalf("tws0: Write error for message %d: %s", i, err)
		}
		if err := twss[max-1].ws.WriteMessage(
			websocket.BinaryMessage, msg); err != nil {
			t.Fatalf("twsN: Write error for message %d: %s", i, err)
		}
	}

	// Tell the timeout goroutine to stop
	allDone <- true

	// Close connections and wait for test goroutines
	for _, tws := range twss {
		tws.close()
	}
	w.Wait()

	// Check everything in the main app finishes
	WG.Wait()
}

func TestHubSeq_ReconnectingClientsDontMissMessages(t *testing.T) {
	// Logging for just this function
	fLog := tLog.New("fn", "TestHubSeq_ReconnectingClientsDontMissMessages")
	fLog.Debug("Entering")

	// Just for this test, lower the reconnectionTimeout so that a
	// Leaver message is triggered reasonably quickly.
	oldReconnectionTimeout := reconnectionTimeout
	reconnectionTimeout = 250 * time.Millisecond
	defer func() {
		reconnectionTimeout = oldReconnectionTimeout
	}()

	// Start a server
	serv := newTestServer(bounceHandler)
	defer serv.Close()

	// Connect two clients. One will listen to messages, and
	// occasionally disconnect and reconnect. The other will send messages.
	// The first client should receive them all.

	game := "/hub.reconnecting"
	listener := sync.WaitGroup{}
	sent, rcvd := []string{}, []string{}
	listenerReady := make(chan bool, 0) // Just closed to signal ready

	// Listen for messages, allowing up 100ms for any to come through

	listener.Add(1)
	go func() {
		defer listener.Done()

		num := -1
		conns := 0
		gotFirstEnv := false
		for {
			ws1, _, err := dial(serv, game, "WS1", num)
			if err != nil {
				ws1.Close()
				t.Fatal(err)
			}
			tws1 := newTConn(ws1, "WS1")
			fLog.Debug("Dialled", "id", "WS1", "num", num)
			conns++

			// Close the connection after some time, which gets
			// longer and longer to ensure we do genuinely (eventually)
			// time out while listening
			go func() {
				closeMs := rand.Intn(50 * conns)
				time.Sleep(time.Duration(closeMs) * time.Millisecond)
				fLog.Debug("Goroutine closed connection", "id", "WS1")
				tws1.close()
			}()

			for {
				rr, timedOut := tws1.readMessage(250)
				if timedOut {
					fLog.Debug("Timed out while reading", "id", "WS1")
					// Assume no more messages
					tws1.close()
					return
				}
				if rr.err != nil {
					// Presume connection is closed
					fLog.Debug("Read error, presume closed",
						"id", "WS1", "err", rr.err.Error())
					tws1.close()
					break
				}
				var env Envelope
				err := json.Unmarshal(rr.msg, &env)
				if err != nil {
					t.Fatal(err)
				}
				num = env.Num
				fLog.Debug("Received", "id", "WS1", "num", num,
					"intent", env.Intent, "content", string(env.Body))
				if !gotFirstEnv {
					fLog.Debug("Listener got first envelope", "id", "WS1")
					gotFirstEnv = true
					// Signal to the sender it can start sending
					close(listenerReady)
				}
				if env.Intent == "Peer" {
					rcvd = append(rcvd, string(env.Body))
				}
			}
		}
	}()

	// Send some messages on one connection, up to 50ms apart

	ws2, _, err := dial(serv, game, "WS2", -1)
	fLog.Debug("Dialled", "id", "WS2", "num", -1)
	if err != nil {
		ws2.Close()
		t.Fatal(err)
	}
	tws2 := newTConn(ws2, "WS2")
	if err := tws2.swallow("Welcome"); err != nil {
		t.Fatal(err)
	}

	// Wait for the listener to be ready
	fLog.Debug("Sender waiting to go", "id", "WS2")
	<-listenerReady

	// Send some messages
	fLog.Debug("Sending messages", "id", "WS2")
	for i := 0; i < 20; i++ {
		time.Sleep(time.Duration(rand.Intn(50)) * time.Millisecond)
		n := len(sent)
		msg := "WS2." + strconv.Itoa(n)
		err := tws2.ws.WriteMessage(websocket.BinaryMessage, []byte(msg))
		if err != nil {
			t.Fatalf(
				"Couldn't write message, msg=%s, error '%s'",
				msg, err.Error(),
			)
		}
		fLog.Debug("Sent message", "id", "WS2", "content", msg)
		sent = append(sent, msg)
		tws2.swallow("Receipt")
	}
	tws2.close()

	// Wait for listener to finish
	listener.Wait()

	// Check what was sent is what was received
	sliceDiff := func(a []string, b []string) string {
		out := ""
		for i := 0; i < maxLength(a, b); i++ {
			switch {
			case i < len(a) && i < len(b):
				if a[i] == b[i] {
					out += fmt.Sprintf("%d: ..... .....", i)
				} else {
					out += fmt.Sprintf("%d: %s %s", i, a[i], b[i])
				}
			case i < len(a):
				out += fmt.Sprintf("%d: %s", i, a[i])
			case i < len(b):
				out += fmt.Sprintf("%d:       %s", i, b[i])
			default:
				out += "Error!!!!"
			}
			out += "\n"
		}
		return out
	}

	if !reflect.DeepEqual(sent, rcvd) {
		t.Errorf("Sent by ws1 != received by ws2. Sent/received:\n%s",
			sliceDiff(sent, rcvd))
	}

	fLog.Debug("Waiting on group")
	WG.Wait()
}

// If a client connects with an existing ID for which there's an old
// client, but it's expecting a message num that's not there, then
// it should be get a closed connection with a suitable message.
func TestHubSeq_ReconnectionWithBadLastnumShouldGetClosed(t *testing.T) {
	// Just for this test, lower the reconnectionTimeout so that a
	// Leaver message is triggered reasonably quickly.
	oldReconnectionTimeout := reconnectionTimeout
	reconnectionTimeout = 250 * time.Millisecond
	defer func() {
		reconnectionTimeout = oldReconnectionTimeout
	}()

	// Start a server
	serv := newTestServer(bounceHandler)
	defer serv.Close()

	// Connect the first client
	game := "/hub.reconn.bad.unsent"
	ws1a, _, err := dial(serv, game, "REC1", -1)
	defer ws1a.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws1a := newTConn(ws1a, "REC1")
	if err := tws1a.swallow("Welcome"); err != nil {
		t.Fatalf("Welcome error for ws1a: %s", err)
	}

	// Connect the second client
	ws2, _, err := dial(serv, game, "REC2", -1)
	defer ws2.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws2 := newTConn(ws2, "REC2")
	if err := tws2.swallow("Welcome"); err != nil {
		t.Fatalf("Welcome error for ws2: %s", err)
	}

	// The first client should get a joiner message. Record the envelope num

	rr, timedOut := tws1a.readMessage(250)
	if timedOut {
		t.Fatal("ws1a timed out listening for Joiner message")
	}
	if rr.err != nil {
		t.Fatalf("ws1a got error listening for Joiner: %s", err.Error())
	}
	env := Envelope{}
	err = json.Unmarshal(rr.msg, &env)
	if err != nil {
		t.Fatalf("ws1a got error unmarshalling: %s", err.Error())
	}
	num := env.Num

	// We'll connect a replacement for ws1a with a lastnum of something that
	// doesn't exist. When we try to read it should get a closed connection
	// with a suitable error message.
	ws1b, _, err := dial(serv, game, "REC1", num+10)
	if err != nil {
		t.Fatalf("Error dialling for ws1b: %s", err)
	}
	defer ws1b.Close()
	tws1b := newTConn(ws1b, "REC1")
	rr, timedOut = tws1b.readMessage(250)
	if timedOut {
		t.Fatal("ws1b timed out listening for message")
	}
	if rr.err == nil {
		t.Fatal("ws1b should have got a closed connection, but didn't")
	}
	if !strings.Contains(rr.err.Error(), "lastnum") {
		t.Errorf("Error message was not suitable: '%s'", rr.err.Error())
	}

	// Close the other connections
	tws1a.close()
	tws2.close()

	// Wait for all processes to finish
	WG.Wait()
}

// If a client connects with an existing ID for which there's an old
// client, and it's expecting a sensible last num but it was too slow,
// then it should be get a closed connection with a suitable message.
func TestHubSeq_ReconnWithGoodLastnumTooLateShouldGetClosed(t *testing.T) {
	// Just for this test, lower the reconnectionTimeout so that a
	// Leaver message is triggered reasonably quickly.
	oldReconnectionTimeout := reconnectionTimeout
	reconnectionTimeout = 200 * time.Millisecond
	defer func() {
		reconnectionTimeout = oldReconnectionTimeout
	}()

	// Start a server
	serv := newTestServer(bounceHandler)
	defer serv.Close()

	// Connect a client and read the num
	game := "/hub.reconn.too.late"
	ws1a, _, err := dial(serv, game, "REC1", -1)
	defer ws1a.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws1a := newTConn(ws1a, "REC1")
	env, err := tws1a.readEnvelope(500)
	if err != nil {
		t.Fatalf("Error reading envelope: %s", err)
	}
	lastnum := env.Num

	// Close the connection, wait too long, then reconnect with a
	// sensible lastnum

	tws1a.close()
	time.Sleep(500 * time.Millisecond)

	ws1b, _, err := dial(serv, game, "REC1", lastnum)
	if err != nil {
		t.Fatalf("Error dialling for ws1b: %s", err)
	}
	defer ws1b.Close()
	tws1b := newTConn(ws1b, "REC1")
	rr, timedOut := tws1b.readMessage(250)
	if timedOut {
		t.Fatal("ws1b timed out listening for expected close")
	}
	if rr.err == nil {
		t.Fatal("ws1b should have got a closed connection, but didn't")
	}
	if !strings.Contains(rr.err.Error(), "lastnum") {
		t.Errorf("Error message was not suitable: '%s'", rr.err.Error())
	}

	// Close the other connections
	tws1a.close()
	tws1b.close()

	// Wait for all processes to finish
	WG.Wait()
}
