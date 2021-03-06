// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

// Simple game server
package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"

	"github.com/inconshreveable/log15"
)

// Global superhub that holds all the hubs
var Shub = NewSuperhub()

// A global wait group, not used in the normal course of things,
// but useful to wait on when debuggging.
var WG = sync.WaitGroup{}

func main() {
	// Set the logger -only for when the application runs, as this is in main
	aLog.SetHandler(
		log15.LvlFilterHandler(
			log15.LvlInfo,
			// log15.LvlDebug,
			// log15.DiscardHandler(),
			log15.StdoutHandler,
		))

	// Handle proof of running
	http.HandleFunc("/", helloHandler)

	// Handle game requests
	http.HandleFunc("/g/", bounceHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		aLog.Info("Using default port", "port", port)
	}

	aLog.Info("Listening", "port", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		aLog.Crit("ListenAndServe", "error", err)
		os.Exit(1)
	}
}

// bounceHandler sets up a websocket to bounce whatever it receives to
// other clients in the same game.
func bounceHandler(w http.ResponseWriter, r *http.Request) {
	// The client will get a response as soon as Upgrade returns, so use
	// the waitgroup to ensure tests wait for all subsequent goroutines.
	WG.Add(1)
	defer WG.Done()

	// Make sure we can get a hub
	hub, err := Shub.Hub(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		aLog.Warn("Superhub rejected client", "path", r.URL.Path, "err", err.Error())
		return
	}

	// Create the client
	ClientID := ClientIDOrNew(r.URL.RawQuery)
	lastNum := lastNum(r.URL.RawQuery)
	num := lastNum
	if lastNum >= 0 {
		num = lastNum + 1
	}
	c := &Client{
		ID:           ClientID,
		Num:          num,
		WS:           nil,
		Hub:          hub,
		InitialQueue: make(chan *Queue),
		Pending:      make(chan *Envelope),
	}
	c.Ref = fmt.Sprintf("%p", c)

	// Try to upgrade to a websocket
	ws, err := upgrader.Upgrade(w, r, make(http.Header))
	if err != nil {
		aLog.Warn("Upgrade error", "error", err)
		Shub.Release(c.Hub, c)
		return
	}
	c.WS = ws

	// Start the client handler running.
	aLog.Info("Connected client", "path", r.URL.Path, "id", c.ID, "ref", c.Ref)
	c.Start()
}

// lastNum gets the integer given by the lastnum query parameter,
// or -1 if there is none.
func lastNum(query string) int {
	v, err := url.ParseQuery(query)
	if err != nil {
		aLog.Warn("Couldn't parse query string", "query", query)
		return -1
	}
	lnStr := v.Get("lastnum")
	if lnStr == "" {
		return -1
	}
	num, err := strconv.Atoi(lnStr)
	if err != nil {
		aLog.Warn("lastnum not an integer", "lastnum", query)
		return -1
	}
	return num
}

// Just say hello
func helloHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	fmt.Fprint(w, "Hello, there")
}
