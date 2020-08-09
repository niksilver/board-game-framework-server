#!/bin/bash

DAY=`date +"%a"`
LOGDIR=/var/log/ssl-proxy
LOGFILENAME=ssl-proxy.$DAY.log

mkdir -p $LOGDIR

THISDIR=`dirname "$0"`

# SSL proxy from https://github.com/suyashkumar/ssl-proxy/releases/
exec "$THISDIR/ssl-proxy" -from 0.0.0.0:443 -to 127.0.0.1:80 -domain=bgf.pigsaw.org >>$LOGDIR/$LOGFILENAME
