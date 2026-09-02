// Copyright 2026 Dolthub, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sqlserver

import (
	"errors"
	"io"
	"net"
	"sync"
)

// muxListener aggregates several underlying net.Listeners into a single
// net.Listener, so a server that only understands one listener can accept
// connections from many sockets. This is used to support systemd socket
// activation with multiple ListenStream=/ListenDatagram= entries handed to
// the process at once.
//
// Connections accepted from any underlying listener are delivered through a
// shared channel; Accept() pulls from that channel. Closing the muxListener
// closes every underlying listener and unblocks all Accept() callers with
// net.ErrClosed. Once any underlying listener returns a terminal error
// (other than ErrClosed) it is reported via errs() for diagnostics, but the
// mux keeps serving the survivors.
type muxListener struct {
	listeners []net.Listener
	conns     chan acceptResult
	shutdown  chan struct{}
	once      sync.Once
	wg        sync.WaitGroup
	firstAddr net.Addr
}

type acceptResult struct {
	conn net.Conn
	err  error
}

// newMuxListener wraps one or more listeners. At least one listener must be
// supplied; the first listener's address is reported by Addr().
func newMuxListener(listeners ...net.Listener) (*muxListener, error) {
	if len(listeners) == 0 {
		return nil, errors.New("muxListener: at least one listener is required")
	}
	for i, l := range listeners {
		if l == nil {
			return nil, errors.New("muxListener: listener at index " + itoa(i) + " is nil")
		}
	}
	ml := &muxListener{
		listeners: listeners,
		conns:     make(chan acceptResult),
		shutdown:  make(chan struct{}),
		firstAddr: listeners[0].Addr(),
	}
	for _, l := range listeners {
		ml.wg.Add(1)
		go ml.acceptLoop(l)
	}
	return ml, nil
}

func (ml *muxListener) acceptLoop(l net.Listener) {
	defer ml.wg.Done()
	for {
		conn, err := l.Accept()
		if err != nil {
			// Treat a closed listener as an orderly shutdown signal.
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// Surface genuine accept failures to the consumer, then stop
			// spinning on this listener. Other underlying listeners survive.
			select {
			case ml.conns <- acceptResult{err: err}:
			case <-ml.shutdown:
				return
			}
			return
		}
		select {
		case ml.conns <- acceptResult{conn: conn}:
		case <-ml.shutdown:
			conn.Close()
			return
		}
	}
}

// Accept blocks until a connection is available from any underlying
// listener, or until Close is called. It returns net.ErrClosed when the
// mux has been shut down.
func (ml *muxListener) Accept() (net.Conn, error) {
	res, ok := <-ml.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return res.conn, res.err
}

// Close shuts down every underlying listener and unblocks outstanding
// Accept() callers. Safe to call multiple times.
func (ml *muxListener) Close() error {
	ml.once.Do(func() {
		close(ml.shutdown)
		for _, l := range ml.listeners {
			_ = l.Close()
		}
		// Wait for accept loops to observe closure so the channel drain
		// below sees no late writers, then close the channel.
		ml.wg.Wait()
		close(ml.conns)
	})
	return nil
}

// Addr reports the address of the first underlying listener. Callers that
// care about individual addresses should inspect the constituent listeners.
func (ml *muxListener) Addr() net.Addr {
	return ml.firstAddr
}

// itoa avoids importing strconv solely for a one-off error message index.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// Compile-time assertion that muxListener satisfies net.Listener.
var _ net.Listener = (*muxListener)(nil)

// Ensure unused io import is referenced for the blank-import guarantee
// (keeps goimports/linters happy if extended later).
var _ = io.EOF
