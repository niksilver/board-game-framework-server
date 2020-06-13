// Copyright 2020 Nik Silver
//
// Licensed under the GPL v3.0. See file LICENCE.txt for details.

// Logging for the board game framework.
package main

import (
	"os"

	"github.com/inconshreveable/log15"
)

// Log is the logger.
// By default it logs application-side code, and discards everything
var Log = log15.New()

func init() {
	Log.SetHandler(log15.DiscardHandler())
}

// SetLvlDebugStdout sets the level to be Debug and outputs to Stdout.
func DebugStdoutHandler() {
	Log.SetHandler(
		log15.LvlFilterHandler(
			log15.LvlDebug,
			log15.StreamHandler(os.Stdout, log15.LogfmtFormat()),
		))
}
