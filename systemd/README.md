# dolt systemd units

This directory ships the pieces needed to run **dolt** as a managed
system service (`dolt.sql-server`):

```
/etc/sysusers.d/dolt.conf      # allocates the static "_dolt" system account
/etc/tmpfiles.d/dolt.conf      # provisions writable runtime directories
/etc/dolt/server.yaml.example  # annotated default sql-server configuration
/etc/systemd/system/dolt.service  # the service unit
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
