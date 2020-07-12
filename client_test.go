// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"github.com/gorilla/websocket"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestClient_CreatesNewID(t *testing.T) {
	// Just for this test, lower the reconnectionTimeout so that a
	// Leaver message is triggered reasonably quickly.
	oldReconnectionTimeout := reconnectionTimeout
	reconnectionTimeout = 250 * time.Millisecond
	defer func() {
		reconnectionTimeout = oldReconnectionTimeout
	}()

	serv := newTestServer(bounceHandler)
	defer serv.Close()

	ws, _, err := dial(serv, "/cl.creates.new.id", "", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws := newTConn(ws, "ws")
	defer tws.close()

	env, err := tws.readEnvelope(500, "Expecting Welcome")
	if err != nil {
		t.Fatal(err)
	}

	clientID := env.To[0]
	if clientID == "" {
		t.Errorf("clientID cookie is empty or not defined")
	}

	// Tidy up, and check everything in the main app finishes
	tws.close()
	WG.Wait()
}

func TestClient_ReusesOldId(t *testing.T) {
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

	initialClientID := "existing_value"

	ws, _, err := dial(serv, "/cl.reuses.old.id", initialClientID, -1)
	if err != nil {
		t.Fatal(err)
	}
	tws := newTConn(ws, initialClientID)
	defer tws.close()

	env, err := tws.readEnvelope(500, "Expecting welcome")
	if err != nil {
		t.Fatal(err)
	}
	clientID := env.To[0]
	if clientID != initialClientID {
		t.Errorf("clientID cookie: expected '%s', got '%s'",
			clientID,
			initialClientID)
	}

	// Tidy up, and check everything in the main app finishes
	tws.close()
	WG.Wait()
}

func TestClient_NewIDsAreDifferent(t *testing.T) {
	usedIDs := make(map[string]bool)
	cIDs := make([]string, 40)
	twss := make([]*tConn, 40)

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

	for i := 0; i < len(twss); i++ {
		// Get a new client connection
		ws, _, err := dial(serv, "/cl.new.ids.different", "", -1)
		if err != nil {
			t.Fatal(err)
		}
		tws := newTConn(ws, "connection"+strconv.Itoa(i))
		twss[i] = tws
		defer twss[i].close()

		// Get the ID in the welcome envelope
		env, err := tws.readEnvelope(500, "Expecting welcome, i=%d", i)
		if err != nil {
			t.Fatal(err)
		}
		clientID := env.To[0]
		cIDs[i] = clientID

		if usedIDs[clientID] {
			t.Errorf("Iteration i = %d, clientID '%s' already used",
				i,
				clientID)
			return
		}
		if clientID == "" {
			t.Errorf("Iteration i = %d, clientID not set", i)
			return
		}

		usedIDs[clientID] = true
	}

	// Tidy up, and check everything in the main app finishes
	for _, tws := range twss {
		tws.close()
	}
	WG.Wait()
}

func TestClient_SendsPings(t *testing.T) {
	// We'll send pings every 500ms, and wait for 3 seconds to receive
	// at least three of them.
	oldPingFreq := pingFreq
	pingFreq = 500 * time.Millisecond
	pings := 0

	// We'll also lower the reconnectionTimeout so that a
	// Leaver message is triggered reasonably quickly.
	oldReconnectionTimeout := reconnectionTimeout
	reconnectionTimeout = 250 * time.Millisecond

	// Start a server
	serv := newTestServer(bounceHandler)

	// Make sure we tidy up after
	defer func() {
		pingFreq = oldPingFreq
		reconnectionTimeout = oldReconnectionTimeout
		serv.Close()
	}()

	ws, _, err := dial(serv, "/cl.sends.pings", "pingtester", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws := newTConn(ws, "pingtester")
	defer tws.close()

	// Signal pings
	pingC := make(chan bool)
	ws.SetPingHandler(func(string) error {
		pingC <- true
		return nil
	})

	// Set a timer for 3 seconds
	timeout := time.After(3 * time.Second)

	// We'll assume the client connects reasonably quickly

	// In the background loop until we get three pings, an error, or a timeout
	w := sync.WaitGroup{}
	w.Add(1)
	go func() {
		defer w.Done()
	pingLoop:
		for {
			select {
			case <-pingC:
				pings += 1
				if pings == 3 {
					break pingLoop
				}
			case <-timeout:
				t.Errorf("Timeout waiting for ping")
				break pingLoop
			}
		}
		ws.Close()
	}()

	// Read the connection, which will listen for pings
	// It will exit with a close error when the above code times out
	tws.readPeerMessage(10_000)

	if pings < 3 {
		t.Errorf("Expected at least 3 pings but got %d", pings)
	}

	w.Wait()

	// Tidy up, and check everything in the main app finishes
	ws.Close()
	WG.Wait()
}

func TestClient_DisconnectsIfNoPongs(t *testing.T) {
	// Give the bounceHandler a very short pong timeout (just for this test)
	oldPongTimeout := pongTimeout
	pongTimeout = 500 * time.Millisecond

	// Lower the reconnectionTimeout so that a
	// Leaver message is triggered reasonably quickly.
	oldReconnectionTimeout := reconnectionTimeout
	reconnectionTimeout = 250 * time.Millisecond

	// Start a server
	serv := newTestServer(bounceHandler)

	// Tidy up after
	defer func() {
		pongTimeout = oldPongTimeout
		reconnectionTimeout = oldReconnectionTimeout
		serv.Close()
	}()

	ws, _, err := dial(serv, "/cl.if.no.pongs", "pongtester", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws := newTConn(ws, "pongtester")
	defer tws.close()

	// Wait for the client to have connected, and swallow the "Welcome"
	// message
	if err := tws.swallow("Welcome"); err != nil {
		t.Fatal(err)
	}

	// Within 3 seconds we should get no message, and the peer should
	// close. It shouldn't time out.
	rr, timedOut := tws.readMessage(3000)
	if timedOut {
		t.Errorf("Too long waiting for peer to close")
	}
	if rr.err == nil {
		t.Errorf("Wrongly got data from peer")
	}

	// Tidy up, and check everything in the main app finishes
	ws.Close()
	WG.Wait()
}

func TestClient_NewClientWithBadLastnumGetsClosedWebsocket(t *testing.T) {
	fLog := tLog.New("fn", "TestClient_NewClientWithBadLastnumGetsClosedWebsocket")

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

	// Connect a client with an unavailable lastnum

	game := "/cl.bad.lastnum"

	// Connect the client with a silly lastnum
	ws, _, err := dial(serv, game, "BAD", 1029)
	if err != nil {
		t.Fatalf("Error dialing BAD: %s", err.Error())
	}
	tws := newTConn(ws, "BAD")
	defer tws.close()
	if err := tws.expectClose(CloseBadLastnum, 500); err != nil {
		t.Errorf("Bad response body: %s", err.Error())
	}

	// Tidy up, and check everything in the main app finishes
	fLog.Debug("Tidying up")
	tws.close()
	WG.Wait()
}

func TestClient_ExcessiveMessageWillCloseConnection(t *testing.T) {
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

	// Connect the client, and consume the welcome message
	ws, _, err := dial(serv, "/cl.excess.message", "EXCESS1", -1)
	defer ws.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws := newTConn(ws, "EXCESS1")
	if err = swallowMany(
		intentExp{"WF1 joining", tws, "Welcome"},
	); err != nil {
		t.Fatal(err)
	}

	// Create 100k message, and send that. It should fail at some point
	msg := make([]byte, 100*1024)
	err = ws.WriteMessage(websocket.BinaryMessage, msg)
	if err != nil {
		// We got an error, and that's probably okay
	}

	// Reading should tell us the connection has been closed by peer
	rr, timedOut := tws.readMessage(500)
	if timedOut {
		t.Fatal("Timed out reading, but should have got an immediate close")
	}
	if rr.err == nil {
		t.Fatal("Didn't get an error reading connection, which is wrong")
	}
	// We got an error, and that's good.
	if websocket.IsCloseError(rr.err, websocket.CloseMessageTooBig) {
		// The right error
	} else {
		t.Errorf(
			"Got an error reading, but it was the wrong one: %s",
			rr.err.Error(),
		)
	}

	// Tidy up, and check everything in the main app finishes
	ws.Close()
	WG.Wait()
}
