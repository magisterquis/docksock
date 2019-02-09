// Program docksock exposes docker sockets to the network
package main

/*
 * docksock.go
 * Expose docker sockets to the network
 * By J. Stuart McMurray
 * Created 20190207
 * Last Modified 20190208
 */

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	/* verbose is the logging function */
	verbose = func(string, ...interface{}) {}

	// ErrNoPortsLeft is returned if there's no more allowed listening
	// ports
	ErrNoPortsLeft = errors.New("no more ports")
)

// CloseWriter is an interface which wraps the CloseWrite method.
type CloseWriter interface {
	CloseWrite() error
}

/* checker is used to pass a context to filepath.Walk */
type walker struct {
	np     uint /* Next port to try to listen on */
	npL    *sync.Mutex
	re     *regexp.Regexp /* Regex which sockets must match */
	slist  string         /* Socket list */
	slistL *sync.Mutex
	seen   map[string]struct{} /* Sockets we know about */
	seenL  *sync.Mutex
}

/* walkFn is called for every walked file */
func (w *walker) walkFn(path string, info os.FileInfo, err error) error {
	/* Don't care about things we can't access */
	if nil != err {
		return nil
	}
	/* Skip /proc, /sys, and /dev */
	if info.IsDir() && (strings.HasPrefix(path, "/proc") ||
		strings.HasPrefix(path, "/sys") ||
		strings.HasPrefix(path, "/dev")) {
		return filepath.SkipDir
	}

	/* Make sure it's a socket */
	if 0 == info.Mode()&os.ModeSocket {
		return nil
	}

	/* Make sure it contains the substring */
	if !w.re.MatchString(path) {
		return nil
	}

	/* If we've already seen this one, don't bother */
	w.seenL.Lock()
	if _, ok := w.seen[path]; ok {
		w.seenL.Unlock()
		return nil
	}

	/* Note that we've seen it now */
	w.seen[path] = struct{}{}
	w.seenL.Unlock()

	/* It's a matching socket, serve it */
	go w.serve(path)

	return nil
}

/* listen listens on the next port.  It may return ErrNoPortsLeft if there are
no more ports left for listening. */
func (w *walker) listen() (net.Listener, error) {
	var (
		l   net.Listener
		err error
	)
	/* Find a port on which to listen */
	for p := w.nextPort(); 0 != p; p = w.nextPort() {
		a := net.JoinHostPort(
			net.IPv4zero.String(),
			fmt.Sprintf("%v", p),
		)
		l, err = net.Listen("tcp", a)
		if nil != err {
			verbose("Cannot listen on %v: %v", a, err)
			continue
		}
		return l, nil
	}
	/* If we still haven't got a port, bummer */
	return nil, ErrNoPortsLeft
}

/* serve proxies tcp connections on the next available port to the socket */
func (w *walker) serve(path string) {

	/* Spawn a listener */
	l, err := w.listen()
	if nil != err {
		verbose("[%v] Unable to make listener: %v", path, err)
		return
	}
	verbose("Listening on %v for connections to %v", l.Addr(), path)
	defer l.Close()

	/* Not the listening socket and path */
	w.slistL.Lock()
	w.slist += fmt.Sprintf("%v -> %v\n", l.Addr(), path)
	w.slistL.Unlock()

	/* Accept and serve clients */
	for {
		c, err := l.Accept()
		if nil != err {
			verbose(
				"Error accepting connection to %v: %v",
				l.Addr(),
				err,
			)
			break
		}
		go w.proxy(c, path)
	}
}

/* proxy proxies between c and the unix socket at path */
func (w *walker) proxy(c net.Conn, path string) {
	defer c.Close()

	tag := fmt.Sprintf("%v -> %v", c.RemoteAddr(), path)
	verbose("[%v] Connected", tag)

	/* Try to connect to the socket */
	s, err := net.Dial("unix", path)
	if nil != err {
		m := fmt.Sprintf(
			"[%v] Unable to connect to socket: %v",
			tag,
			err,
		)
		verbose("%s", m)
		fmt.Fprintf(c, "%s", m)
		return
	}

	/* Proxy bytes back and forth */
	var (
		pwg sync.WaitGroup
		fn  int64
		rn  int64
	)
	pwg.Add(2)

	go func() {
		defer pwg.Done()
		if cw, ok := c.(CloseWriter); ok {
			defer cw.CloseWrite()
		}
		n, err := io.Copy(c, s)
		if nil != err && io.EOF != err {
			verbose(
				"[%v] Error sending data to %v: %v",
				tag,
				c.RemoteAddr(),
				err,
			)
		}
		rn = n
	}()
	go func() {
		defer pwg.Done()
		if cw, ok := s.(CloseWriter); ok {
			defer cw.CloseWrite()
		}
		n, err := io.Copy(s, c)
		if nil != err && io.EOF != err {
			verbose(
				"[%v] Error sending data from %v: %v",
				tag,
				path,
				err,
			)
		}
		fn = n
	}()
	pwg.Wait()

	verbose("[%v] Done.  %v bytes forward, %v bytes back.", tag, fn, rn)
}

/* nextPort returns the next port in w */
func (w *walker) nextPort() uint {
	w.npL.Lock()
	defer w.npL.Unlock()
	p := w.np
	w.np++
	w.np %= 65536
	return p
}

/* serveList serves the list of listening sockets.  It closes ready when it's
ready to serve */
func (w *walker) serveList(ready chan<- struct{}) {
	/* Spawn a listener */
	l, err := w.listen()
	if nil != err {
		verbose("Unable to listen for list queries: %v", err)
		os.Exit(1)
	}
	verbose("Listening on %v for list queries", l.Addr())
	defer l.Close()
	close(ready)

	/* Service queries for lists */
	for {
		c, err := l.Accept()
		if nil != err {
			verbose("Unable to accept list query client: %v", err)
			break
		}
		go func(lc net.Conn) {
			defer lc.Close()
			w.slistL.Lock()
			s := w.slist
			if "" == s {
				s = "none yet\n"
			}
			w.slistL.Unlock()
			fmt.Fprintf(lc, "%s", s)
			verbose("[%v] List query", lc.RemoteAddr())
		}(c)
	}
}

func main() {
	var (
		re = flag.String(
			"path-re",
			"ssh|docker|tmux|tmp",
			"Socket paths must match the `regex` to be served",
		)
		startPort = flag.Uint(
			"start-port",
			51111,
			"Starting `port` to use for socket service",
		)
		startDir = flag.String(
			"top-dir",
			"/",
			"Topmost `directory` in which to search for sockets",
		)
		logOn = flag.Bool(
			"v",
			false,
			"Verbose logging",
		)
		scanInterval = flag.Duration(
			"scan-interval",
			5*time.Minute,
			"Time to `wait` between scans for new sockets",
		)
	)
	flag.Usage = func() {
		fmt.Fprintf(
			os.Stderr,
			`Usage: %v [options]

Finds unix sockets matching a regex and for each found socket, listens on a TCP
port and forwards connections to the Unix socket.  Every so often the
filesystem is scanned for new sockets.

The first port will send a list of port -> socket mappings to any connecting
client.

Options:
`,
			os.Args[0],
		)
		flag.PrintDefaults()
	}
	flag.Parse()

	/* Disable logging, maybe */
	if *logOn {
		log.SetOutput(os.Stdout)
		verbose = log.Printf
	}

	/* Walk context */
	w := &walker{
		np:     *startPort,
		npL:    new(sync.Mutex),
		slistL: new(sync.Mutex),
		seen:   make(map[string]struct{}),
		seenL:  new(sync.Mutex),
	}
	var err error
	w.re, err = regexp.Compile(*re)
	if nil != err {
		verbose("Error compiling regex: %v", err)
	}

	/* Serve up a list of sockets */
	ready := make(chan struct{})
	go w.serveList(ready)
	<-ready

	/* Look for sockets every so often */
	for {
		if err := filepath.Walk(*startDir, w.walkFn); nil != err {
			log.Fatalf("Error walking file tree: %v", err)
		}
		time.Sleep(*scanInterval)
	}

}
