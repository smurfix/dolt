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
	"net"
	"strings"

	"github.com/coreos/go-systemd/v22/activation"
	"github.com/sirupsen/logrus"
)

// activatedSockets groups the net.Listeners handed to this process by
// systemd socket activation, partitioned by role. Roles are derived from
// the socket units' FileDescriptorName setting:
//
//	"<name>.sql"        -> MySQL listeners (may be several)
//	"<name>.metrics"    -> Prometheus /metrics HTTP listener
//	"<name>.remotesapi" -> remotesapi listener
//
// Unnamed listeners (legacy LISTEN_FDS without LISTEN_FDNAMES) are assigned
// to the MySQL role, preserving compatibility with the simplest possible
// socket unit.
type activatedSockets struct {
	sql        []net.Listener
	metrics    []net.Listener
	remotesapi []net.Listener
}

// isActive reports whether any socket-activated file descriptors were
// received. When false, the server falls back to binding its own listeners
// from configuration.
func (a *activatedSockets) isActive() bool {
	return a != nil && (len(a.sql)+len(a.metrics)+len(a.remotesapi) > 0)
}

// loadActivatedSockets collects the systemd-passed listeners exactly once
// (the activation package clears LISTEN_* from the environment on first
// call). Partitioning by name suffix keeps the door open for several socket
// units fronting different protocols.
func loadActivatedSockets() activatedSockets {
	out := activatedSockets{}
	withNames, err := activation.ListenersWithNames()
	if err != nil {
		logrus.Warnf("socket activation: could not enumerate inherited listeners: %v", err)
		return out
	}
	for name, listeners := range withNames {
		role := classifySocketName(name)
		logrus.Infof("socket activation: %d listener(s) for role %q (name=%q)", len(listeners), role, name)
		switch role {
		case "sql":
			out.sql = append(out.sql, listeners...)
		case "metrics":
			out.metrics = append(out.metrics, listeners...)
		case "remotesapi":
			out.remotesapi = append(out.remotesapi, listeners...)
		default:
			logrus.Warnf("socket activation: ignoring listener with unrecognized name %q", name)
		}
	}
	return out
}

// classifySocketName maps a FileDescriptorName to a role token. The default
// (including the synthetic "LISTEN_FD_<n>" names produced when
// LISTEN_FDNAMES is absent) is "sql", so a barebones socket unit Just Works.
func classifySocketName(name string) string {
	switch {
	case name == "":
		return "sql"
	case strings.HasSuffix(name, ".sql"):
		return "sql"
	case strings.HasSuffix(name, ".metrics"):
		return "metrics"
	case strings.HasSuffix(name, ".remotesapi"):
		return "remotesapi"
	default:
		return "sql"
	}
}
