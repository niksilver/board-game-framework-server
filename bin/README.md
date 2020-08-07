# Running the server as a service

For a linux server that uses `systemctl`:

```
go build -o boardgameframework
mv boardgameframework bin
chmod a+x bin/boardgameframework.sh
chmod a+x bin/ssl-proxy
chmod a+x bin/ssl-proxy.sh
cp -r bin ..
cd ../bin
```

Install the service config, start it now, and ensure it starts on future reboots:

```
sudo cp boardgameframework.service /etc/systemd/system
sudo systemctl start boardgameframework
sudo systemctl enable boardgameframework
```

Do the same for the SSL proxy:

```
sudo cp ssl-proxy.service /etc/systemd/system
sudo systemctl start ssl-proxy
sudo systemctl enable ssl-proxy
```

The log file is in `/var/log/boardgameframework`.
