// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"encoding/json"
	"strconv"
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
	if err != nil {
		t.Fatal(err)
	}
	tws := newTConn(ws, "WTESTER")
	defer tws.close()

	// Read the next message, expected within 500ms
	env, err := tws.readEnvelope(500, "Waiting for welcome")
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

	room := "/hub.welcome.from.existing"

	// Connect the first client, and consume the welcome message
	ws1, _, err := dial(serv, room, "WF1", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws1 := newTConn(ws1, "WF1")
	defer tws1.close()
	if err = swallowMany(
		intentExp{"WF1 joining, ws1", tws1, "Welcome"},
	); err != nil {
		t.Fatal(err)
	}

	// Connect the second client, and consume intro messages
	ws2, _, err := dial(serv, room, "WF2", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws2 := newTConn(ws2, "WF2")
	defer tws2.close()
	if err = swallowMany(
		intentExp{"WF2 joining, ws2", tws2, "Welcome"},
		intentExp{"WF2 joining, ws1", tws1, "Joiner"},
	); err != nil {
		t.Fatal(err)
	}

	// Connect the third client
	ws3, _, err := dial(serv, room, "WF3", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws3 := newTConn(ws3, "WF3")
	defer tws3.close()

	// Get what we expect to be the the welcome message
	env, err := tws3.readEnvelope(500, "tws3")
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

	room := "/hub.basic.envelope"

	// We'll want to check From, To and Time fields, as well as
	// message contents.
	// Because we have 3 clients we'll have 2 listed in the To field.

	// Client 1 joins normally

	ws1, _, err := dial(serv, room, "EN1", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws1 := newTConn(ws1, "EN1")
	defer tws1.close()
	if err := tws1.swallow("Welcome"); err != nil {
		t.Fatalf("Welcome error for ws1: %s", err)
	}

	// Client 2 joins, and client 1 gets a joiner message

	ws2, _, err := dial(serv, room, "EN2", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws2 := newTConn(ws2, "EN2")
	defer tws2.close()
	if err = swallowMany(
		intentExp{"EN2 joining, ws2", tws2, "Welcome"},
		intentExp{"EN2 joining, ws1", tws1, "Joiner"},
	); err != nil {
		t.Fatal(err)
	}

	// Client 3 joins, and clients 1 and 2 get joiner messages.

	ws3, _, err := dial(serv, room, "EN3", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws3 := newTConn(ws3, "EN3")
	defer tws3.close()
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

	// Read the message
	env, err := tws2.readEnvelope(500, "tws2")
	if err != nil {
		t.Fatal(err)
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

	// Time field. First convert milliseconds back to Time(!)
	timeT := time.Unix(env.Time/1000, env.Time%1000)
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

	room := "/hub.joiner.messages"

	// Connect the first client
	ws1, _, err := dial(serv, room, "JM1", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws1 := newTConn(ws1, "JM1")
	defer tws1.close()
	if err := tws1.swallow("Welcome"); err != nil {
		t.Fatalf("Welcome error for ws1: %s", err)
	}

	// Connect the second client
	ws2, _, err := dial(serv, room, "JM2", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws2 := newTConn(ws2, "JM2")
	defer tws2.close()
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
	ws3, _, err := dial(serv, room, "JM3", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws3 := newTConn(ws3, "JM3")
	defer tws3.close()
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
	if env.Time > nowMs() {
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

	room := "/hub.leaver.messages"

	// Connect the first client
	ws1, _, err := dial(serv, room, "LV1", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws1 := newTConn(ws1, "LV1")
	defer tws1.close()
	if err := tws1.swallow("Welcome"); err != nil {
		t.Fatalf("Welcome error for ws1: %s", err)
	}

	// Connect the second client
	ws2, _, err := dial(serv, room, "LV2", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws2 := newTConn(ws2, "LV2")
	defer tws2.close()
	if err = swallowMany(
		intentExp{"LV2 joining, ws2", tws2, "Welcome"},
		intentExp{"LV2 joining, ws1", tws1, "Joiner"},
	); err != nil {
		t.Fatal(err)
	}

	// Connect the third client
	ws3, _, err := dial(serv, room, "LV3", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws3 := newTConn(ws3, "JM3")
	defer tws3.close()
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
	if env.Time > nowMs() {
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
	if env.Time > nowMs() {
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

	// Our expected clients, allowing some connections to fail
	maxTries := 2 * MaxClients
	twss := make([]*tConn, maxTries)

	// Start a web server
	serv := newTestServer(bounceHandler)
	defer serv.Close()

	// A client should consume messages until done.
	// We'll use a wait group to ensure we move on only when all clients
	// are done, and a concurrency-safe counter to ensure we get to
	// max number of clients (allowing some to fail, which shouldn't
	// happen but does, sometimes).

	w := sync.WaitGroup{}
	c := conCounter{}

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
		c.dec()
	}

	// Let 50 clients join the game, but allow for some to fail
	// (which shouldn't happen, but sometimes does)
	i := 0
	for c.get() < MaxClients && i < maxTries {
		id := "MAX" + strconv.Itoa(i)
		ws, _, err := dial(serv, "/hub.max", id, -1)
		tws := newTConn(ws, id)
		if err != nil {
			t.Fatalf("Couldn't dial, i=%d, error '%s'", i, err.Error())
		}
		defer tws.close()
		twss[i] = tws
		w.Add(1)
		c.inc()
		go consume(tws, id)
		i++
	}

	// Check we've stopped because we've got the max number of clients,
	// not because we gave up.
	if c.get() < MaxClients {
		t.Fatalf("Couldn't connect %d clients; tried %d times", MaxClients, i)
	}

	// Trying to connect should get a response, but an error response
	// from the upgraded websocket connection.

	ws, resp, err := dial(serv, "/hub.max", "MAXOVER", -1)
	if err == nil {
		t.Fatalf("Expected error for MAXOVER, but didn't get one")
	}
	if err := responseContains(resp, "Maximum number of clients"); err != nil {
	}

	// Close connections and wait for test goroutines
	for _, tws := range twss {
		if tws != nil {
			tws.close()
		}
	}
	if ws != nil {
		ws.Close()
	}
	w.Wait()

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

	room := "/hub.time.ms"

	// Connect the first client and get the time from the welcome message
	ws1, _, err := dial(serv, room, "TIM1", -1)
	if err != nil {
		t.Fatal(err)
	}
	tws1 := newTConn(ws1, "TIM1")
	defer tws1.close()

	env, err := tws1.readEnvelope(500, "tws1 welcome")
	if err != nil {
		t.Fatal(err)
	}
	if env.Intent != "Welcome" {
		t.Fatalf("tws1: Expected Welcome intent, but got %s", env.Intent)
	}
	time1 := env.Time

	// Wait 100ms and connect the second client
	time.Sleep(100 * time.Millisecond)
	ws2, _, err := dial(serv, room, "TIM2", -1)
	defer ws2.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Get the time from the Joiner message
	env, err = tws1.readEnvelope(500, "tws1 joiner")
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

	tws1.close()
	ws2.Close()
	tLog.Debug("TestHubMsgs_TimeIsInMilliseconds, waiting on group")
	WG.Wait()
}
