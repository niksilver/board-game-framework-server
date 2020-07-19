# Running the server as a service

For a linux server that uses `systemctl`:

1 Put this `bin` directory in `/home/bgf`.
1 Ensure the shell wrapper is executable: `chmod a+x boardgameframework.sh`.
1 Copy the compiled server binary `boardgameframework` into this directory.

Install the service config, start it now, and ensure it starts on future reboots:

```
sudo cp boardgameframework.service /etc/systemd/system
sudo systemctl start boardgameframework
sudo systemctl enable boardgameframework
```

Log files are per day, in directory `/var/log/boardgameframework`.
