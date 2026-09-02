#!/bin/sh
# Install the dolt systemd integration onto the filesystem.
#
# Each asset goes where systemd actually loads it from, all rooted under
# $DESTDIR/lib (plus the config example under $DESTDIR/etc):
#
#   dolt.service          -> $DESTDIR/lib/systemd/system/dolt.service
#   dolt.sysusers         -> $DESTDIR/lib/sysusers.d/dolt.conf
#   dolt.tmpfiles         -> $DESTDIR/lib/tmpfiles.d/dolt.conf
#   server.yaml.example   -> $DESTDIR/etc/dolt/server.yaml.example
#
# sysusers.d / tmpfiles.d ignore files lacking a ".conf" suffix, hence the
# rename on install. Units are distro-shipped under lib/systemd/system
# (NOT /etc/systemd/system, which is reserved for administrator overrides).
#
# NOTE: this script deliberately does NOT create /etc/dolt/server.yaml.
# The service unit carries ConditionPathExists=/etc/dolt/server.yaml, so it
# will refuse to start until an administrator copies the example to
# server.yaml and reviews it. Run that step yourself (see "Activate" below).
#
# Usage:
#   sudo ./INSTALL.sh                 # DESTDIR=/usr (default)
#   DESTDIR=./staging ./INSTALL.sh    # stage into a build tree
#
# Re-runnable; overwrites the unit/snippets with the current copies and
# leaves any pre-existing /etc/dolt/server.yaml untouched.
set -eu

# Resolve this script's directory so it finds its sibling assets no matter
# the caller's CWD. readlink -f is a GNU-ism; the target is Linux/systemd.
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

DESTDIR=${DESTDIR:-/usr}
# Normalise: strip any trailing slashes so joins stay clean.
while [ "${DESTDIR%/}" != "$DESTDIR" ] && [ -n "$DESTDIR" ]; do
    DESTDIR=${DESTDIR%/}
done
: "${DESTDIR:=/}"

UNIT_DIR=$DESTDIR/lib/systemd/system
SYSUSERS_DIR=$DESTDIR/lib/sysusers.d
TMPFILES_DIR=$DESTDIR/lib/tmpfiles.d
CFG_DIR=$DESTDIR/etc/dolt

# Source assets, keyed to their shipped filenames.
SERVICE_SRC=$SCRIPT_DIR/dolt.service
SYSUSERS_SRC=$SCRIPT_DIR/dolt.sysusers
TMPFILES_SRC=$SCRIPT_DIR/dolt.tmpfiles
CONFIG_EXAMPLE_SRC=$SCRIPT_DIR/server.yaml.example

# Sanity-check the bundle is intact before touching the filesystem.
for f in "$SERVICE_SRC" "$SYSUSERS_SRC" "$TMPFILES_SRC" "$CONFIG_EXAMPLE_SRC"; do
    [ -f "$f" ] || { echo "INSTALL.sh: missing asset: $f" >&2; exit 1; }
done

echo "Installing dolt systemd integration into $DESTDIR"

install -D -m0644 "$SERVICE_SRC"        "$UNIT_DIR/dolt.service"
install -D -m0644 "$SYSUSERS_SRC"        "$SYSUSERS_DIR/dolt.conf"
install -D -m0644 "$TMPFILES_SRC"        "$TMPFILES_DIR/dolt.conf"
install -D -m0644 "$CONFIG_EXAMPLE_SRC"  "$CFG_DIR/server.yaml.example"

cat <<EOF

Done. Installed:
  $UNIT_DIR/dolt.service
  $SYSUSERS_DIR/dolt.conf
  $TMPFILES_DIR/dolt.conf
  $CFG_DIR/server.yaml.example

Before activating, an administrator MUST create the config the service gates on:
  install -d -m0750 -o root -g _dolt $DESTDIR/etc/dolt
  cp $CFG_DIR/server.yaml.example $CFG_DIR/server.yaml   # then EDIT it

Activate on a live system (skip when staging into a DESTDIR build root):
  systemd-sysusers $SYSUSERS_DIR/dolt.conf
  systemd-tmpfiles --create $TMPFILES_DIR/dolt.conf
  systemctl daemon-reload
  systemctl enable --now dolt.service
EOF
