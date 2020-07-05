// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

package main

import (
	"os"

	"github.com/inconshreveable/log15"
)

// A logger for application-side logging
var aLog = log15.New("side", "app")

// tLog is a logger for our tests only.
var tLog = log15.New("side", "test")

// uLog is a logger for our these utils only.
var uLog = log15.New("side", "utils")

func init() {
	// These settings are overridden in main()
	// Application-side logging
	aLog.SetHandler(
		log15.LvlFilterHandler(
			// log15.LvlError,
			log15.LvlInfo,
			// log15.LvlDebug,
			// log15.DiscardHandler(),
			log15.StdoutHandler,
			// FlushingStdoutHandler{},
		))

	// Test-side logging
	tLog.SetHandler(
		log15.LvlFilterHandler(
			// log15.LvlWarn,
			log15.LvlDebug,
			log15.DiscardHandler(),
			// FlushingStdoutHandler{},
		))

	// Util-side logging
	uLog.SetHandler(
		log15.LvlFilterHandler(
			// log15.LvlWarn,
			log15.LvlDebug,
			log15.DiscardHandler(),
			// FlushingStdoutHandler{},
		))
}

// log15 stdout handler that always flushes its contents to disk
type FlushingStdoutHandler struct {
}

func (h FlushingStdoutHandler) Log(r *log15.Record) error {
	if err := log15.StdoutHandler.Log(r); err != nil {
		return err
	}
	return os.Stdout.Sync()
}
