# dolt systemd units

This directory ships the pieces needed to run **dolt** as a managed
system service (`dolt.sql-server`):

```
/etc/sysusers.d/dolt.conf      # allocates the static "_dolt" system account
/etc/tmpfiles.d/dolt.conf      # provisions writable runtime directories
/etc/dolt/server.yaml.example  # annotated default sql-server configuration
/usr/lib/systemd/system/dolt.service  # the service unit (Type=notify)
/usr/lib/systemd/system/dolt.socket  # optional socket-activation unit (MySQL role)
/usr/lib/systemd/system/dolt-metrics.socket  # ...and the /metrics role
```

## Layout

| Path            | Purpose                                                        |
| --------------- | ------------------------------------------------------------- |
| `/etc/dolt`     | Admin-owned (`root:_dolt`, 0750) configuration home: holds `server.yaml` and per-instance config. |
| `/var/lib/dolt` | The sql-server `--data-dir` (`_dolt:_dolt`, 0750): dolt repositories live here, one DB per sub-directory. Owned by the unit's `StateDirectory=`. |
| `/run/dolt`     | Per-boot runtime scratch (sockets, locks, …). Owned by the unit's `RuntimeDirectory=`. |

## Installation

The easiest path is the bundled installer, which routes each asset to the
directory systemd actually scans (and applies the `.conf` rename that
sysusers.d/tmpfiles.d require):

```sh
sudo systemd/INSTALL.sh        # DESTDIR defaults to /usr
```

Or, equivalently, copy the files into place by hand (your distribution
packager normally does this for you):

```sh
install -m0644 systemd/dolt.sysusers        /usr/lib/sysusers.d/dolt.conf
install -m0644 systemd/dolt.tmpfiles       /usr/lib/tmpfiles.d/dolt.conf
install -m0644 systemd/dolt.service        /usr/lib/systemd/system/dolt.service
install -m0644 systemd/dolt.socket          /usr/lib/systemd/system/dolt.socket
install -m0644 systemd/dolt-metrics.socket  /usr/lib/systemd/system/dolt-metrics.socket
install -m0644 systemd/server.yaml.example /etc/dolt/server.yaml.example

# Materialise the system user and the /etc/dolt directory:
systemd-sysusers /usr/lib/sysusers.d/dolt.conf
systemd-tmpfiles --create /usr/lib/tmpfiles.d/dolt.conf

systemctl daemon-reload
```

### Mandatory: create the config before starting

The service unit gates startup on `ConditionPathExists=/etc/dolt/server.yaml`,
so it **will not start** until an administrator installs a config. Copy the
annotated example and review it:

```sh
install -m0640 -o root -g _dolt systemd/server.yaml.example /etc/dolt/server.yaml
# ...then edit /etc/dolt/server.yaml (listener port, TLS, privileges, …)

systemctl enable --now dolt.service
```

Until `server.yaml` exists, `systemctl start dolt` is a silent no-op
(reported as `condition-result=no`), not a crash — protecting against the
service coming up with wrong defaults.

## Socket activation (optional)

`dolt.socket` lets systemd pre-bind the listening sockets and hand them to
the service via `LISTEN_FDS` (see `sd_listen_fds(3)`). The service detects
them at startup and serves over them, **ignoring the configured `host`/
`port`/`socket`** for whichever roles it received sockets for.

### One socket unit per role

systemd assigns a **single** `FileDescriptorName=` to *all* sockets in a
`[Socket]` section — the directive may not be repeated, and it cannot vary
per `ListenStream=`/`ListenSpecial=` line. Distinct roles therefore need
**distinct socket units**, each carrying its own `FileDescriptorName=`.
The package ships two:

| Unit file            | `FileDescriptorName=` | Role                                              |
| -------------------- | --------------------- | ------------------------------------------------- |
| `dolt.socket`        | `dolt.sql`            | MySQL listeners (inet **and/or** unix; many OK)   |
| `dolt-metrics.socket`| `dolt.metrics`        | Prometheus `/metrics` HTTP (first listener only)   |

Within `dolt.socket` you may freely combine `ListenStream=` (TCP) and
`ListenSpecial=` (unix) lines — every socket in that unit shares the
`dolt.sql` name and dolt serves them all concurrently via an internal
fan-in listener. So loopback-TCP *and* a unix socket from one unit works
out of the box.

dolt routes FDs to roles by the **suffix** of the name it receives
(`*.sql` → MySQL, `*.metrics` → metrics, `*.remotesapi` → future). FDs
whose name carries no recognized suffix default to the MySQL role, so a
barebones single unit with no `FileDescriptorName=` still works for the
simple all-MySQL case.

```sh
systemctl enable --now dolt.socket            # MySQL listeners
systemctl enable --now dolt-metrics.socket     # /metrics (optional)
# service starts lazily on first connect
```

The service unit does **not** `Require=` the socket, so
`systemctl enable --now dolt.service` still works standalone (binding its
own listeners from `server.yaml`) when you don't want activation. Edit the
socket units to change which sockets systemd pre-binds; add more
`*-metrics.socket`/`*-sql.socket` style units if you need additional
roles or bind addresses.

## Configuration

The service reads `/etc/dolt/server.yaml` by passing it to
`dolt sql-server --config`. An annotated copy lives at
`/etc/dolt/server.yaml.example`; copy it to `server.yaml` and adjust
`listener.port`, TLS, privileges, replication, etc. Administrators
(root) own and edit the config; the `_dolt` service reads it via the
shared `_dolt` group.

Startup is gated on that file's existence (see Installation above), so
reviewing the config is not optional — the service stays down until you do.

Because the binary consults `DOLT_ROOT_PATH` before `$HOME`,
application-global state (credentials cache, global config) is kept
under `/var/lib/dolt/.dolt` rather than polluting `/etc/dolt`.
