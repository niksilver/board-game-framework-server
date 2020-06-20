// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Tests for basic messages and message structure

func TestHubMsgs_SendsWelcome(t *testing.T) {
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

	// Connect to the server
	ws, _, err := dial(serv, "/hub.sends.welcome", "WTESTER", -1)
	defer ws.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws := newTConn(ws, "WTESTER")

	// Read the next message, expected within 500ms
	rr, timedOut := tws.readMessage(500)
	if timedOut {
		t.Fatal("Timed out waiting for welcome message")
	}
	if rr.err != nil {
		t.Fatalf("Error waiting for welcome message: %s", rr.err.Error())
	}

	// Unwrap the message and check it

	env := Envelope{}
	err = json.Unmarshal(rr.msg, &env)
	if err != nil {
		t.Fatal(err)
	}

	if env.Intent != "Welcome" {
		t.Errorf("Message intent was '%s' but expected 'Welcome'", env.Intent)
	}
	if !sameElements(env.To, []string{"WTESTER"}) {
		t.Errorf(
			"Message To field was %v but expected [\"WTESTER\"]",
			env.To,
		)
	}

	// Tidy up, and check everything in the main app finishes
	ws.Close()
	WG.Wait()
}

func TestHubMsgs_WelcomeIsFromExistingClients(t *testing.T) {
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

	// Connect 3 clients in turn. Each existing client should
	// receive a joiner message about each new client.

	game := "/hub.welcome.from.existing"

	// Connect the first client, and consume the welcome message
	ws1, _, err := dial(serv, game, "WF1", -1)
	defer ws1.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws1 := newTConn(ws1, "WF1")
	if err = swallowMany(
		intentExp{"WF1 joining, ws1", tws1, "Welcome"},
	); err != nil {
		t.Fatal(err)
	}

	// Connect the second client, and consume intro messages
	ws2, _, err := dial(serv, game, "WF2", -1)
	defer ws2.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws2 := newTConn(ws2, "WF2")
	if err = swallowMany(
		intentExp{"WF2 joining, ws2", tws2, "Welcome"},
		intentExp{"WF2 joining, ws1", tws1, "Joiner"},
	); err != nil {
		t.Fatal(err)
	}

	// Connect the third client
	ws3, _, err := dial(serv, game, "WF3", -1)
	defer ws3.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws3 := newTConn(ws3, "WF3")

	// Get what we expect to be the the welcome message
	rr, timedOut := tws3.readMessage(500)
	if timedOut {
		t.Fatal("Timed out reading message from ws3")
	}
	if rr.err != nil {
		t.Fatal(err)
	}

	// Unwrap the message and check it

	env := Envelope{}
	err = json.Unmarshal(rr.msg, &env)
	if err != nil {
		t.Fatal(err)
	}

	if env.Intent != "Welcome" {
		t.Errorf("Message intent was '%s' but expected 'Welcome'", env.Intent)
	}
	if !sameElements(env.From, []string{"WF1", "WF2"}) {
		t.Errorf(
			"Message From field was %v but expected it to be [WF1, WF2]",
			env.From,
		)
	}

	// Tidy up, and check everything in the main app finishes
	ws1.Close()
	ws2.Close()
	ws3.Close()
	WG.Wait()
}

func TestHubMsgs_BasicMessageEnvelopeIsCorrect(t *testing.T) {
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

	// Connect 3 clients.
	// We'll make sure all the clients have been added to hub, and force
	// the order by waiting on messages.

	game := "/hub.basic.envelope"

	// We'll want to check From, To and Time fields, as well as
	// message contents.
	// Because we have 3 clients we'll have 2 listed in the To field.

	// Client 1 joins normally

	ws1, _, err := dial(serv, game, "EN1", -1)
	defer ws1.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws1 := newTConn(ws1, "EN1")
	if err := tws1.swallow("Welcome"); err != nil {
		t.Fatalf("Welcome error for ws1: %s", err)
	}

	// Client 2 joins, and client 1 gets a joiner message

	ws2, _, err := dial(serv, game, "EN2", -1)
	defer ws2.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws2 := newTConn(ws2, "EN2")
	if err = swallowMany(
		intentExp{"EN2 joining, ws2", tws2, "Welcome"},
		intentExp{"EN2 joining, ws1", tws1, "Joiner"},
	); err != nil {
		t.Fatal(err)
	}

	// Client 3 joins, and clients 1 and 2 get joiner messages.

	ws3, _, err := dial(serv, game, "EN3", -1)
	defer ws3.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws3 := newTConn(ws3, "EN3")
	if err = swallowMany(
		intentExp{"EN3 joining, ws3", tws3, "Welcome"},
		intentExp{"EN3 joining, ws1", tws1, "Joiner"},
		intentExp{"EN3 joining, ws2", tws2, "Joiner"},
	); err != nil {
		t.Fatal(err)
	}

	// Send a message, then pick up the results from one of the clients

	err = ws1.WriteMessage(
		websocket.BinaryMessage, []byte("Can you read me?"),
	)
	if err != nil {
		t.Fatalf("Error writing message: %s", err.Error())
	}

	rr, timedOut := tws2.readMessage(500)
	if timedOut {
		t.Fatal("Timed out trying to read message")
	}
	if rr.err != nil {
		t.Fatalf("Error reading message: %s", rr.err.Error())
	}

	env := Envelope{}
	err = json.Unmarshal(rr.msg, &env)
	if err != nil {
		t.Fatalf(
			"Couldn't unmarshal message '%s'. Error %s",
			rr.msg, err.Error(),
		)
	}

	// Test fields...

	// Body field
	if string(env.Body) != "Can you read me?" {
		t.Errorf("Envelope body not as expected, got '%s'", env.Body)
	}

	// From field
	if !sameElements(env.From, []string{"EN1"}) {
		t.Errorf("Got envelope From '%s' but expected ['EN1']", env.From)
	}

	// To field
	if !sameElements(env.To, []string{"EN2", "EN3"}) {
		t.Errorf(
			"Envelope To was '%v' but expected it be just EN2 and EN3",
			env.To,
		)
	}

	// Time field
	timeT := time.Unix(env.Time, 0) // Convert seconds back to Time(!)
	now := time.Now()
	recentPast := now.Add(-5 * time.Second)
	if timeT.Before(recentPast) || timeT.After(now) {
		t.Errorf(
			"Got time %v, which wasn't between %v and %v",
			timeT, recentPast, now,
		)
	}

	// Intent field
	if string(env.Intent) != "Peer" {
		t.Errorf("Envelope intent not as expected, got '%s', expected 'Peer", env.Intent)
	}

	// Tidy up and check everything in the main app finishes
	tLog.Debug("TestHubMsgs_BasicMessageEnvelopeIsCorrect, closing off")
	tws1.close()
	tws2.close()
	tws3.close()
	tLog.Debug("TestHubMsgs_BasicMessageEnvelopeIsCorrect, waiting on group")
	WG.Wait()
}

func TestHubMsgs_JoinerMessagesHappen(t *testing.T) {
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

	// Connect 3 clients in turn. Each existing client should
	// receive a joiner message about each new client.

	game := "/hub.joiner.messages"

	// Connect the first client
	ws1, _, err := dial(serv, game, "JM1", -1)
	defer ws1.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws1 := newTConn(ws1, "JM1")
	if err := tws1.swallow("Welcome"); err != nil {
		t.Fatalf("Welcome error for ws1: %s", err)
	}

	// Connect the second client
	ws2, _, err := dial(serv, game, "JM2", -1)
	defer ws2.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws2 := newTConn(ws2, "JM2")
	if err := tws2.swallow("Welcome"); err != nil {
		t.Fatalf("Welcome error for ws2: %s", err)
	}

	// Expect a joiner message to ws1
	rr, timedOut := tws1.readMessage(500)
	if timedOut {
		t.Fatal("Timed out reading tws1")
	}
	if rr.err != nil {
		t.Fatal(err)
	}
	var env Envelope
	err = json.Unmarshal(rr.msg, &env)
	if err != nil {
		t.Fatal(err)
	}
	if env.Intent != "Joiner" {
		t.Fatalf("ws1 message isn't a joiner message. env is %#v", env)
	}
	if !sameElements(env.From, []string{"JM2"}) {
		t.Fatalf("ws1 got From field which wasn't JM2. env is %#v", env)
	}
	if !sameElements(env.To, []string{"JM1"}) {
		t.Fatalf("ws1 To field didn't contain just its ID. env is %#v", env)
	}
	if env.Time < time.Now().Unix() {
		t.Fatalf("ws1 got Time field in the past. env is %#v", env)
	}
	if env.Body != nil {
		t.Fatalf("ws1 got unexpected Body field. env is %#v", env)
	}

	// Expect no message to ws2
	err = tws2.expectNoMessage(500)
	if err != nil {
		t.Fatal(err)
	}

	// Connect the third client
	ws3, _, err := dial(serv, game, "JM3", -1)
	defer ws3.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws3 := newTConn(ws3, "JM3")
	if err := tws3.swallow("Welcome"); err != nil {
		t.Fatalf("Welcome error for tws3: %s", err)
	}

	// Expect a joiner message to ws1 (and shortly, ws2)
	rr, timedOut = tws1.readMessage(500)
	if timedOut {
		t.Fatal("Timed out reading tws1")
	}
	if rr.err != nil {
		t.Fatal(rr.err)
	}
	err = json.Unmarshal(rr.msg, &env)
	if err != nil {
		t.Fatal(err)
	}
	if env.Intent != "Joiner" {
		t.Fatalf("ws1 message isn't a joiner message. env is %#v", env)
	}
	if !sameElements(env.From, []string{"JM3"}) {
		t.Fatalf("ws1 got From field not with just JM3. env is %#v", env)
	}
	if !sameElements(env.To, []string{"JM1", "JM2"}) {
		t.Fatalf("ws1 To field didn't contain JM1 and JM2. env is %#v", env)
	}
	if env.Time > time.Now().Unix() {
		t.Fatalf("ws1 got Time field in the future. env is %#v", env)
	}
	if env.Body != nil {
		t.Fatalf("ws1 got unexpected Body field. env is %#v", env)
	}

	// Now check the joiner message to ws2
	rr, timedOut = tws2.readMessage(500)
	if timedOut {
		t.Fatal("Timed out reading tws2")
	}
	if rr.err != nil {
		t.Fatal(rr.err)
	}
	err = json.Unmarshal(rr.msg, &env)
	if err != nil {
		t.Fatal(err)
	}
	if env.Intent != "Joiner" {
		t.Fatalf("ws2 message isn't a joiner message. env is %#v", env)
	}
	if !sameElements(env.From, []string{"JM3"}) {
		t.Fatalf("ws2 got From field not with JM3. env is %#v", env)
	}
	if !sameElements(env.To, []string{"JM2", "JM1"}) {
		t.Fatalf("ws2 To field didn't contain JM1 and JM2. env is %#v", env)
	}
	if env.Time < time.Now().Unix() {
		t.Fatalf("ws2 got Time field in the past. env is %#v", env)
	}
	if env.Body != nil {
		t.Fatalf("ws2 got unexpected Body field. env is %#v", env)
	}

	// Expect no message to ws3
	err = tws3.expectNoMessage(500)
	if err != nil {
		t.Fatal(err)
	}

	// Close the remaining connections and wait for all goroutines to finish
	tws1.close()
	tws2.close()
	tws3.close()
	WG.Wait()
}

func TestHubMsgs_LeaverMessagesHappen(t *testing.T) {
	serv := newTestServer(bounceHandler)
	defer serv.Close()

	// Just for this test, lower the reconnectionTimeout so that a
	// Leaver message is triggered reasonably quickly.

	oldReconnectionTimeout := reconnectionTimeout
	reconnectionTimeout = 250 * time.Millisecond
	defer func() {
		reconnectionTimeout = oldReconnectionTimeout
	}()

	// Connect 3 clients in turn. When one leaves the remaining
	// ones should get leaver messages.

	game := "/hub.leaver.messages"

	// Connect the first client
	ws1, _, err := dial(serv, game, "LV1", -1)
	defer ws1.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws1 := newTConn(ws1, "LV1")
	if err := tws1.swallow("Welcome"); err != nil {
		t.Fatalf("Welcome error for ws1: %s", err)
	}

	// Connect the second client
	ws2, _, err := dial(serv, game, "LV2", -1)
	defer ws2.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws2 := newTConn(ws2, "LV2")
	if err = swallowMany(
		intentExp{"LV2 joining, ws2", tws2, "Welcome"},
		intentExp{"LV2 joining, ws1", tws1, "Joiner"},
	); err != nil {
		t.Fatal(err)
	}

	// Connect the third client
	ws3, _, err := dial(serv, game, "LV3", -1)
	defer ws3.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws3 := newTConn(ws3, "JM3")
	if err = swallowMany(
		intentExp{"LV3 joining, ws3", tws3, "Welcome"},
		intentExp{"LV3 joining, ws1", tws1, "Joiner"},
		intentExp{"LV3 joining, ws2", tws2, "Joiner"},
	); err != nil {
		t.Fatal(err)
	}

	// Now ws1 will leave, and the others should get leaver messages
	// once the reconnectionTimeout has expired
	tws1.close()

	// Let's check the ws2 first
	rr, timedOut := tws2.readMessage(500)
	if timedOut {
		t.Fatal("Timed out reading tws1")
	}
	if rr.err != nil {
		t.Fatal(rr.err)
	}
	var env Envelope
	err = json.Unmarshal(rr.msg, &env)
	if err != nil {
		t.Fatal(err)
	}
	if env.Intent != "Leaver" {
		t.Fatalf("ws2 message isn't a leaver message. env is %#v", env)
	}
	if !sameElements(env.From, []string{"LV1"}) {
		t.Fatalf("ws2 got From field not with just LV1. env is %#v", env)
	}
	if !sameElements(env.To, []string{"LV2", "LV3"}) {
		t.Fatalf("ws2 To field didn't contain LV2 and LV3. env is %#v", env)
	}
	if env.Time > time.Now().Unix() {
		t.Fatalf("ws2 got Time field in the future. env is %#v", env)
	}
	if env.Body != nil {
		t.Fatalf("ws2 got unexpected Body field. env is %#v", env)
	}

	// Now check the leaver message to ws3
	rr, timedOut = tws3.readMessage(500)
	if timedOut {
		t.Fatal("Timed out reading tws1")
	}
	if rr.err != nil {
		t.Fatal(rr.err)
	}
	err = json.Unmarshal(rr.msg, &env)
	if err != nil {
		t.Fatal(err)
	}
	if env.Intent != "Leaver" {
		t.Fatalf("ws3 message isn't a leaver message. env is %#v", env)
	}
	if !sameElements(env.From, []string{"LV1"}) {
		t.Fatalf("ws3 got From field not with just LV1. env is %#v", env)
	}
	if !sameElements(env.To, []string{"LV2", "LV3"}) {
		t.Fatalf("ws3 To field didn't contain LV2 and LV3. env is %#v", env)
	}
	if env.Time > time.Now().Unix() {
		t.Fatalf("ws3 got Time field in the future. env is %#v", env)
	}
	if env.Body != nil {
		t.Fatalf("ws3 got unexpected Body field. env is %#v", env)
	}

	// Close the remaining connections and wait for all goroutines to finish
	tws2.close()
	tws3.close()
	WG.Wait()
}

func TestHubMsgs_SendsErrorOverMaximumClients(t *testing.T) {
	// Just for this test, lower the reconnectionTimeout so that a
	// Leaver message is triggered reasonably quickly.

	oldReconnectionTimeout := reconnectionTimeout
	reconnectionTimeout = 250 * time.Millisecond
	defer func() {
		reconnectionTimeout = oldReconnectionTimeout
	}()

	// Our expected maximum clients
	twss := make([]*tConn, MaxClients)

	// Start a web server
	serv := newTestServer(bounceHandler)
	defer serv.Close()

	// A client should consume messages until done
	w := sync.WaitGroup{}
	consume := func(tws *tConn, id string) {
		defer w.Done()
		for {
			rr, timedOut := tws.readMessage(500)
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

	// Let 50 clients join the game
	for i := 0; i < MaxClients; i++ {
		id := "MAX" + strconv.Itoa(i)
		ws, _, err := dial(serv, "/hub.max", id, -1)
		tws := newTConn(ws, id)
		defer tws.close()
		if err != nil {
			t.Fatalf("Couldn't dial, i=%d, error '%s'", i, err.Error())
		}
		twss[i] = tws
		w.Add(1)
		go consume(tws, id)
	}

	// Trying to connect should get a response, but an error response
	// from the upgraded websocket connection.

	ws, _, err := dial(serv, "/hub.max", "MAXOVER", -1)
	if err != nil {
		t.Fatalf("Failed network connection: %s", err)
	}
	tws := newTConn(ws, "MAXOVER")
	defer tws.close()

	// Should not be able to read now
	rr, timedOut := tws.readMessage(500)
	if timedOut {
		t.Fatal("Timed out reading connection that should have given error")
	}
	if rr.err == nil {
		t.Fatalf("No error reading message")
	}
	if !strings.Contains(rr.err.Error(), "Maximum number of clients") {
		t.Errorf("Got error, but the wrong one: %s", rr.err.Error())
	}

	// Close connections and wait for test goroutines
	for _, tws := range twss {
		tws.close()
	}
	w.Wait()
	tws.close()

	// Check everything in the main app finishes
	WG.Wait()
}

func TestHubMsgs_TimeIsInMilliseconds(t *testing.T) {
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

	// Start two clients, 100ms apart. The first should receive a joiner
	// message about the second approx 100ms after it received its own
	// welcome message

	game := "/hub.time.ms"

	// Connect the first client and get the time from the welcome message
	ws1, _, err := dial(serv, game, "TIM1", -1)
	defer ws1.Close()
	if err != nil {
		t.Fatal(err)
	}
	tws1 := newTConn(ws1, "TIM1")
	rr, timedOut := tws1.readMessage(500)
	if timedOut {
		t.Fatal("tws1: Timed out waiting for welcome message")
	}
	if rr.err != nil {
		t.Fatalf("tws1: Got error instead of welcome message")
	}
	env := Envelope{}
	err = json.Unmarshal(rr.msg, &env)
	if err != nil {
		t.Fatal(err)
	}
	if env.Intent != "Welcome" {
		t.Fatalf("tws1: Expected Welcome intent, but got %s", env.Intent)
	}
	time1 := env.Time

	// Wait 100ms and connect the second client
	time.Sleep(100 * time.Millisecond)
	ws2, _, err := dial(serv, game, "TIM2", -1)
	defer ws2.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Get the time from the Joiner message
	rr, timedOut = tws1.readMessage(500)
	if timedOut {
		t.Fatal("tws1: Timed out waiting for joiner message")
	}
	if rr.err != nil {
		t.Fatalf("tws1: Got error instead of joiner message")
	}
	err = json.Unmarshal(rr.msg, &env)
	if err != nil {
		t.Fatal(err)
	}
	if env.Intent != "Joiner" {
		t.Fatalf("tws1: Expected Joiner intent, but got %s", env.Intent)
	}
	time2 := env.Time

	// Check the time difference, but allow for some delays
	timeDiff := time2 - time1
	if !(90 <= timeDiff && timeDiff <= 200) {
		t.Errorf("Expected 90 <= timeDiff <= 200, but got timeDiff %d",
			timeDiff)
	}
}
