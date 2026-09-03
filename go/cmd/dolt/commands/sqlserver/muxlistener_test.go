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
	"io"
	"net"
	"testing"
	"time"
)

// openListener returns a loopback TCP listener on a free port.
func openListener(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return l
}

// dialOpened dials the given listener and returns the established conn.
func dialOpened(t *testing.T, l net.Listener) net.Conn {
	t.Helper()
	c, err := net.Dial(l.Addr().Network(), l.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return c
}

// TestMuxListenerEmptyRejects guards against silent footguns.
func TestMuxListenerEmptyRejects(t *testing.T) {
	if _, err := newMuxListener(); err == nil {
		t.Fatal("expected error constructing mux with no listeners")
	}
}

// TestMuxListenerNilRejects ensures a nil constituent is caught eagerly.
func TestMuxListenerNilRejects(t *testing.T) {
	good := openListener(t)
	defer good.Close()
	if _, err := newMuxListener(good, nil); err == nil {
		t.Fatal("expected error constructing mux with a nil listener")
	}
}

// TestMuxFanIn proves connections from several underlying listeners arrive
// through the single muxed Accept() — the property go-mysql-server relies on.
func TestMuxFanIn(t *testing.T) {
	l1, l2, l3 := openListener(t), openListener(t), openListener(t)
	defer l1.Close()
	defer l2.Close()
	defer l3.Close()

	ml, err := newMuxListener(l1, l2, l3)
	if err != nil {
		t.Fatalf("newMuxListener: %v", err)
	}
	defer ml.Close()

	// Dial each underlying listener; expect each accepted conn to surface
	// from the mux in arrival order (channel is unbuffered/FIFO).
	for _, l := range []net.Listener{l1, l2, l3} {
		client := dialOpened(t, l)
		defer client.Close()

		accepted, aerr := ml.Accept()
		if aerr != nil {
			t.Fatalf("Accept: %v", aerr)
		}
		defer accepted.Close()
		if accepted.RemoteAddr() == nil {
			t.Fatal("accepted conn has no remote addr")
		}
		// Echo a byte round-trip to confirm a real, usable connection.
		if _, err := client.Write([]byte{7}); err != nil {
			t.Fatalf("client write: %v", err)
		}
		buf := make([]byte, 1)
		if _, err := io.ReadFull(accepted, buf); err != nil {
			t.Fatalf("server read: %v", err)
		}
		if buf[0] != 7 {
			t.Fatalf("expected 7, got %d", buf[0])
		}
	}
}

// TestMuxCloseUnblocksAccept verifies that Close releases any goroutine
// parked in Accept(), returning net.ErrClosed.
func TestMuxCloseUnblocksAccept(t *testing.T) {
	l := openListener(t)
	defer l.Close()
	ml, err := newMuxListener(l)
	if err != nil {
		t.Fatalf("newMuxListener: %v", err)
	}

	acceptErr := make(chan error, 1)
	go func() {
		_, e := ml.Accept()
		acceptErr <- e
	}()
	// Give the accepter a moment to park before we close.
	time.Sleep(50 * time.Millisecond)

	if err := ml.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case e := <-acceptErr:
		if e != net.ErrClosed {
			t.Fatalf("expected net.ErrClosed, got %v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("Accept did not unblock within 1s of Close")
	}
}

// TestMuxSurvivesPartialFailure simulates one underlying listener dying
// while others keep serving; the mux must keep delivering conns from the
// survivor.
func TestMuxSurvivesPartialFailure(t *testing.T) {
	live := openListener(t)
	dead := openListener(t)
	defer live.Close()
	ml, err := newMuxListener(dead, live)
	if err != nil {
		t.Fatalf("newMuxListener: %v", err)
	}
	defer ml.Close()

	// Kill one listener out from under the mux.
	dead.Close()

	// A connection to the surviving listener must still be accepted.
	client := dialOpened(t, live)
	defer client.Close()
	accepted, aerr := ml.Accept()
	if aerr != nil {
		t.Fatalf("Accept after partial failure: %v", aerr)
	}
	defer accepted.Close()
}

// TestMuxAddrReportsFirst confirms Addr() surfaces the first listener,
// which go-mysql-server uses for system-variable bookkeeping.
func TestMuxAddrReportsFirst(t *testing.T) {
	first := openListener(t)
	second := openListener(t)
	defer first.Close()
	defer second.Close()
	ml, err := newMuxListener(first, second)
	if err != nil {
		t.Fatalf("newMuxListener: %v", err)
	}
	defer ml.Close()
	if ml.Addr().String() != first.Addr().String() {
		t.Fatalf("Addr mismatch: got %s want %s", ml.Addr().String(), first.Addr().String())
	}
}

// TestClassifySocketName pins the name->role mapping that drives partition.
func TestClassifySocketName(t *testing.T) {
	cases := map[string]string{
		"":                  "sql",
		"LISTEN_FD_3":       "sql",
		"dolt.sql":          "sql",
		"dolt-west.sql":     "sql", // region-prefixed variant still routes to MySQL
		"foo.bar.sql":       "sql",
		"dolt.metrics":      "metrics",
		"dolt-eu.metrics":   "metrics",
		"dolt.remotesapi":   "remotesapi",
		"dolt.socket":       "sql", // systemd default name (unit basename) when FileDescriptorName= is unset
		"dolt-metrics.socket": "sql", // ditto: no .metrics suffix -> default role
		"just-some-name":    "sql", // unknown defaults to sql
	}
	for name, want := range cases {
		if got := classifySocketName(name); got != want {
			t.Errorf("classifySocketName(%q) = %q, want %q", name, got, want)
		}
	}
}
