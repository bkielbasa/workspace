#!/bin/sh
# Creates the connecting user's home directory if missing, so first
# contact with a fresh account succeeds instead of failing tree connect.
# Only strict lowercase emails/dots/dashes/plus/underscore/at reach mkdir;
# anything else is rejected before it can touch the shell (this runs as
# root via preexec with the login name substituted into the command).
set -eu

name="${1:-}"
case "$name" in
	""|*[!a-z0-9._@+-]*)
		exit 0
		;;
esac

dir="/data/files/$name"
[ -d "$dir" ] || mkdir -p "$dir"
