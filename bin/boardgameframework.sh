#!/bin/bash

DAY=`date +"%a"`
LOGDIR=/var/log/boardgameframework
LOGFILENAME=boardgameframework.$DAY.log

mkdir -p $LOGDIR

THISDIR=`dirname "$0"`

# SSL proxy from https://github.com/suyashkumar/ssl-proxy/releases/
"$THISDIR/ssl-proxy" -from 0.0.0.0:443 -to 127.0.0.1:8080 -domain=bgf.pigsaw.org >>$LOGDIR/$LOGFILENAME &

# Start the board game framework server
export PORT=8080
exec "$THISDIR/boardgameframework" >>$LOGDIR/$LOGFILENAME
