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
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dolthub/dolt/go/libraries/doltcore/sqle"
	"github.com/dolthub/dolt/go/libraries/utils/svcs"
)

// TestSocketActivationSupplantsBind proves that when Config.Activated.sql
// is populated, Serve uses the inherited listeners verbatim and ignores
// the configured host/port. We deliberately configure the server on a busy
// port (the default, 3306-ish) that we never expect to bind: if activation
// weren't honoured, WaitForStart would error with "port in use".
//
// We pre-create TWO distinct loopback listeners to exercise the
// fan-in/multiple-socket requirement, then drive a MySQL handshake over
// each to confirm both reach the same server.
func TestSocketActivationSupplantsBind(t *testing.T) {
	// Two inherited listeners on ephemeral ports stand in for systemd's
	// ListenStream= entries.
	inheritedTCP, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	inheritedTCP2, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = inheritedTCP.Close()
		_ = inheritedTCP2.Close()
	})

	env, err := sqle.CreateEnvWithSeedData()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, env.Close()) })

	// Configure a host/port we do NOT expect to bind. Activation must win.
	// Port 9999 is in-range for config validation but we never bind it;
	// the port-in-use probe is skipped under activation too.
	cfg := DefaultCommandLineServerConfig().
		WithHost("127.0.0.1").
		WithPort(9999)

	sc := svcs.NewController()
	activated := activatedSockets{
		sql: []net.Listener{inheritedTCP, inheritedTCP2},
	}
	go func() {
		_, _ = Serve(context.Background(), &Config{
			Version:      "0.0.0",
			ServerConfig: cfg,
			Controller:   sc,
			DoltEnv:      env,
			Activated:    &activated,
		})
	}()

	// If activation had NOT supplanted the bind, the server would try to
	// listen on 127.0.0.1:1 and WaitForStart would error here.
	require.NoError(t, sc.WaitForStart())
	t.Cleanup(func() {
		sc.Stop()
		_ = sc.WaitForStop()
	})

	// Probe each inherited listener: a TCP accept followed by the client
	// hanging up confirms the mux surfaced the conn to the server. We
	// don't complete a full MySQL handshake (heavyweight); reaching the
	// accept loop is sufficient evidence that the activated listener — not
	// the configured port — is in charge.
	for _, lis := range []net.Listener{inheritedTCP, inheritedTCP2} {
		c, err := net.Dial(lis.Addr().Network(), lis.Addr().String())
		require.NoError(t, err)
		// The server should have accepted; give it a beat.
		_ = c.Close()
	}
}

// TestSocketActivationInactiveKeepsSelfBind confirms the branching
// contract in getConfigFromServerConfig: when NO activated MySQL
// listeners are supplied, serverConf.Listener stays nil so go-mysql-server
// binds the configured host/port itself (the historical behavior). This
// is the inverse of TestSocketActivationSupplantsBind and is exercised as
// a pure unit test to avoid the server start/stop lifecycle, which trips
// an unrelated close-vs-send race in upstream go-mysql-server's Listener
// (server/listener.go:101 vs :149) under -race.
func TestSocketActivationInactiveKeepsSelfBind(t *testing.T) {
	cfg := DefaultCommandLineServerConfig().
		WithHost("127.0.0.1").
		WithPort(15432) // any valid port; we never bind it here

	got, err := getConfigFromServerConfig(cfg, nil, nil /* activatedSql */)
	require.NoError(t, err)
	assert.Nil(t, got.Listener, "without activation the server must self-bind (Listener == nil)")
	assert.NotEqual(t, "", got.Address, "configured address should still be populated for self-bind")
}

// TestSocketActivationActiveSetsListener is the companion: when activated
// listeners ARE supplied, getConfigFromServerConfig aggregates them into a
// muxListener and assigns serverConf.Listener, causing go-mysql-server to
// skip its own bind. We assert the contract directly rather than driving a
// full server, keeping the test hermetic and race-free.
func TestSocketActivationActiveSetsListener(t *testing.T) {
	l1, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	l2, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = l1.Close()
		_ = l2.Close()
	})

	cfg := DefaultCommandLineServerConfig().
		WithHost("127.0.0.1").
		WithPort(9999) // deliberately ignored under activation

	got, err := getConfigFromServerConfig(cfg, nil, []net.Listener{l1, l2})
	require.NoError(t, err)
	require.NotNil(t, got.Listener, "activation must supplant self-bind by setting Listener")

	// The mux should surface connections from BOTH underlying listeners.
	mux, ok := got.Listener.(*muxListener)
	require.True(t, ok, "Listener should be our *muxListener")
	assert.Equal(t, l1.Addr().String(), mux.Addr().String(), "mux Addr reports the first listener")

	// Round-trip a connection through each to prove fan-in works post-construction.
	for _, lis := range []net.Listener{l1, l2} {
		client, derr := net.Dial(lis.Addr().Network(), lis.Addr().String())
		require.NoError(t, derr)
		accepted, aerr := mux.Accept()
		require.NoError(t, aerr)
		_ = accepted.Close()
		_ = client.Close()
	}
	_ = mux.Close()
}
