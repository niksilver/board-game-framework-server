// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"strconv"
	"testing"
	"time"
)

func TestBuffer_BasicAddNextFromInitialisation(t *testing.T) {
	// Initial buffer
	buf := NewBuffer()
	if buf.HasUnsent() {
		t.Error("Initial buffer shouldn't have unsent message, but apparently it has")
	}
	if _, err := buf.Next(); err == nil {
		t.Error("Initial buffer doesn't have next, but got no error")
	}

	// With one message
	buf.Add(&Envelope{
		Num:    0,
		Intent: "intent_0",
	})
	if buf.HasUnsent() {
		t.Error("Shouldn't say it has one unsent message, as we've not set the unsent num yet")
	}
	if _, err := buf.Next(); err == nil {
		t.Error("buf.Next() should give error if unsent num not set")
	}
	buf.Set(0)
	if !buf.HasUnsent() {
		t.Error("After setting the unsent num, we should have an unsent message")
	}
	env0, err := buf.Next()
	if err != nil {
		t.Errorf("Should have been able to get next envelope, but got error %s", err.Error())
	}
	if env0.Intent != "intent_0" {
		t.Errorf("First envelope intent should be intent_0 but got %s", env0.Intent)
	}
	if buf.HasUnsent() {
		t.Error("Should have no unsent messages after getting only envelope")
	}
	if _, err := buf.Next(); err == nil {
		t.Error("Initial buffer should have given error after getting only envelope")
	}

	// Two more messages
	buf.Add(&Envelope{
		Num:    1,
		Intent: "intent_1",
	})
	buf.Add(&Envelope{
		Num:    2,
		Intent: "intent_2",
	})
	if !buf.HasUnsent() {
		t.Error("Buffer should tell us it's got the unsent messages we've just added")
	}
	env1, err := buf.Next()
	if err != nil {
		t.Errorf("Getting next should not give error, but got %s", err.Error())
	}
	if env1.Intent != "intent_1" {
		t.Errorf("Expected envelope with intent intent_1, but got %s", env1.Intent)
	}
	if !buf.HasUnsent() {
		t.Error("Buffer should tell us it's got the second unsent message we added")
	}
	env2, err := buf.Next()
	if err != nil {
		t.Errorf("Getting next should not give error, but got %s", err.Error())
	}
	if env2.Intent != "intent_2" {
		t.Errorf("Expected envelope with intent intent_2, but got %s", env2.Intent)
	}

	// Finally, with no more messages
	if buf.HasUnsent() {
		t.Error("There shouldn't be unsent messages")
	}
	if _, err := buf.Next(); err == nil {
		t.Error("Getting next should give error, but it didn't")
	}
}

func TestBuffer_GettingEnvelopeThatIsntThere(t *testing.T) {
	buf := NewBuffer()
	buf.Add(&Envelope{
		Num:    1001,
		Intent: "intent_1001",
	})
	buf.Add(&Envelope{
		Num:    1003,
		Intent: "intent_1003",
	})
	buf.Add(&Envelope{
		Num:    1004,
		Intent: "intent_1004",
	})
	buf.Set(1001)

	// First get should be okay
	if !buf.HasUnsent() {
		t.Error("Expected to hear there is an unsent envelope")
	}
	env1001, err := buf.Next()
	if err != nil {
		t.Errorf("Should have been able to get next, but got error %s", err.Error())
	}
	if env1001.Intent != "intent_1001" {
		t.Errorf("Should have got envelope with intent_1001, but got %s", env1001.Intent)
	}

	// Following get should not be okay
	if buf.HasUnsent() {
		t.Error("Should have reported the expected envelope is not present")
	}
	env1002, err := buf.Next()
	if err == nil {
		t.Error("Should not have been able to get 1002, but no error returned")
	}
	if env1002 != nil {
		t.Errorf("Should not have been able to get 1002, but got %v", env1002)
	}

	// Further gets should also fail
	if buf.HasUnsent() {
		t.Error("Should have reported again the expected envelope is not present")
	}
	env1002a, err := buf.Next()
	if err == nil {
		t.Error("Should not have been able to get next, but no error returned")
	}
	if env1002a != nil {
		t.Errorf("Should not have been able to get next, but got %v", env1002a)
	}
}

func TestBuffer_Cleaning(t *testing.T) {
	oldReconnectionTimeout := reconnectionTimeout
	defer func() {
		reconnectionTimeout = oldReconnectionTimeout
	}()

	reconnectionTimeout = 500 * time.Millisecond

	// Add lots of envelopes going from 600ms in the future (because that's
	// how long we'll pause for cleaning) to 5 seconds before then
	buf := NewBuffer()
	now := time.Now().UnixNano()/1_000_000 + 600
	start := now - 5000
	maxenvs := int64(100)
	inc := 5000 / maxenvs
	num := 1001
	numOld := -1    // Should not be able to get this; not yet set
	numRecent := -1 // Should be able to get this; not yet set
	for t := start; t < now; t += inc {
		buf.Add(&Envelope{
			Num:    num,
			Time:   t,
			Intent: "intent_" + strconv.Itoa(num),
		})
		if now-t >= 800 {
			// The latest envelope num that's 800ms old
			numOld = num
		}
		if numRecent == -1 && now-t < 500 {
			// The earliest envelope num that's 500ms old
			numRecent = num
		}
		num += 1
	}

	// At this pre-cleaning stage, getting an old env should be successful
	buf.Set(numOld)
	if !buf.HasUnsent() {
		t.Error("Old unsent envelope should be available")
	}
	envOld, err := buf.Next()
	if err != nil {
		t.Errorf("Getting next old unsent envelope gave error %s", err.Error())
	}
	if envOld == nil {
		t.Error("Got old unsent envelope, but it was nil")
	}

	// Run the cleaning for just 600ms
	buf.Start()
	time.Sleep(600 * time.Millisecond)
	buf.Stop()

	// There should be no envelopes that are 800ms old
	if buf.HasUnsent() {
		t.Error("After cleaning, buffer still has old unsent envelope")
	}
	envOld, err = buf.Next()
	if err == nil {
		t.Error("Error expected getting old envelope, but none returned")
	}
	if envOld != nil {
		t.Errorf("Got old envelope even after cleaning: %v", envOld)
	}

	// There should still be some recent envelopes
	buf.Set(numRecent)
	if !buf.HasUnsent() {
		t.Error("Recent unsent envelope should be available")
	}
	envRecent, err := buf.Next()
	if err != nil {
		t.Errorf("Getting next recent unsent envelope gave error %s", err.Error())
	}
	if envRecent == nil {
		t.Error("Got recent unsent envelope, but it was nil")
	}

	// Wait for all the goroutines to finish
	WG.Wait()
}

func TestBuffer_CleaningEdgeCases(t *testing.T) {
	// Cleaning a just-initialised buffer should be fine
	buf1 := NewBuffer()
	buf1.Clean()

	// Starting periodic cleaning twice should be fine
	buf2 := NewBuffer()
	buf2.Start()
	buf2.Start()
	buf2.Stop()
	WG.Wait()

	// Stopping periodic cleaning that's not started should be fine
	buf3 := NewBuffer()
	buf3.Stop()
	WG.Wait()

	// Stopping periodic cleaning twice should be fine
	buf4 := NewBuffer()
	buf4.Start()
	buf4.Stop()
	buf4.Stop()
	WG.Wait()

	// Cleaning while periodic cleaning should be fine
	buf5 := NewBuffer()
	buf5.Start()
	buf5.Clean()
	buf5.Stop()
	WG.Wait()
}

func TestBuffer_SaveFailCases(t *testing.T) {
	// Save with empty buffer should fail
	buf := NewBuffer()
	if buf.Save(10) == true {
		t.Error("Should not have been able to save with empty buffer")
	}

	// Save for message that's been cleaned should fail
	buf.Add(&Envelope{
		Num:    1001,
		Intent: "intent_1001",
	})
	buf.Add(&Envelope{
		Num:    1002,
		Intent: "intent_1002",
	})
	buf.Add(&Envelope{
		Num:    1003,
		Intent: "intent_1003",
	})
	if buf.Save(1000) == true {
		t.Error("Should not have been able to save cleaned message")
	}

	// Save for message that's not yet arrived should fail
	if buf.Save(1005) == true {
		t.Error("Should not have been able to save message that's not there")
	}
}

func TestBuffer_SaveSuccessFollowedByOneOffClean(t *testing.T) {
	// Just for this test, lower the reconnectionTimeout so that a
	// Leaver message is triggered reasonably quickly.

	oldReconnectionTimeout := reconnectionTimeout
	reconnectionTimeout = 100 * time.Millisecond
	defer func() {
		reconnectionTimeout = oldReconnectionTimeout
	}()

	// Simply saving a message that's there should be okay, and it
	// shouldn't get cleaned up, even if Clean() is called

	buf := NewBuffer()
	now := time.Now().UnixNano() / 1_000_000
	buf.Add(&Envelope{
		Num:    1001,
		Time:   now,
		Intent: "intent_1001",
	})
	buf.Add(&Envelope{
		Num:    1002,
		Time:   now,
		Intent: "intent_1002",
	})
	buf.Add(&Envelope{
		Num:    1003,
		Time:   now,
		Intent: "intent_1003",
	})
	buf.Add(&Envelope{
		Num:    1004,
		Time:   now,
		Intent: "intent_1004",
	})
	if buf.Save(1002) == false {
		t.Error("Should have been able to save message")
	}

	// This would ordinarily clean all the above, but 1002 onwards should
	// be saved
	time.Sleep(2 * reconnectionTimeout)
	buf.Clean()
	buf.Set(1002)
	_, err := buf.Next()
	if err != nil {
		t.Error("Should have been able to get saved message")
	}
	buf.Set(1001)
	_, err = buf.Next()
	if err == nil {
		t.Error("Message before saved message should have been cleaned")
	}
}
