#!/bin/bash

DAY=`date +"%a"`
LOGDIR=/var/log/boardgameframework
LOGFILENAME=boardgameframework.$DAY.log

mkdir -p $LOGDIR

THISDIR=`dirname "$0"`

# Start the board game framework server
export PORT=80
exec "$THISDIR/boardgameframework" >>$LOGDIR/$LOGFILENAME
