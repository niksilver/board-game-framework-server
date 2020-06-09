// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"testing"
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
