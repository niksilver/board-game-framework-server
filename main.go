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

	"github.com/gorilla/websocket"
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

	// Handle command for cookie annulment
	http.HandleFunc("/cmd/annul-cookie", annulCookieHandler)

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

	// Create a websocket connection
	clientID := ClientIDOrNew(r.Cookies())
	ws, err := Upgrade(w, r, clientID)
	if err != nil {
		aLog.Warn("Upgrade", "error", err)
		return
	}

	// Make sure we can get a hub
	hub, err := Shub.Hub(r.URL.Path)
	if err != nil {
		msg := websocket.FormatCloseMessage(
			websocket.CloseNormalClosure, err.Error())
		// The following calls may error, but we're exiting, so will ignore
		ws.WriteMessage(websocket.CloseMessage, msg)
		ws.Close()
		aLog.Warn("Superhub rejected client", "path", r.URL.Path, "err", err)
		return
	}

	// Start the client handler running
	lastNum := lastNum(r.URL.RawQuery)
	num := lastNum
	if lastNum >= 0 {
		num = lastNum + 1
	}
	c := &Client{
		ID:           clientID,
		Num:          num,
		WS:           ws,
		Hub:          hub,
		InitialQueue: make(chan *Queue),
		Pending:      make(chan *Envelope),
	}
	c.Ref = fmt.Sprintf("%p", c)
	c.Start()
	aLog.Info("Connected client", "id", clientID, "num", num, "ref", c.Ref)
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

// annulCookieHandler sets up a websocket, annuls the client ID cookie,
// and closes.
func annulCookieHandler(w http.ResponseWriter, r *http.Request) {
	// Create a websocket connection with an empty cookie
	ws, err := Upgrade(w, r, "")
	if err != nil {
		aLog.Warn("Upgrade", "error", err)
		return
	}
	ws.Close()
	aLog.Info("Annulled cookie")
}

// Just say hello
func helloHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	fmt.Fprint(w, "Hello, there")
}
