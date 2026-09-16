#!/bin/sh
# Ensures a UNIX account exists for an SMB login. Samba validates passdb
# entries against the system user database even with a file backend, so
# every login needs a (locked-down, shell-less) system account.
#
# Called at boot for every smbpasswd row and every 30s by the entrypoint
# loop, so runtime-added logins work with no restarts and no reloads.
# Only lowercase emails/dots/dashes/plus/underscore/at are accepted; anything
# else is rejected before it can reach useradd.
set -eu

name="${1:-}"
case "$name" in
	""|*[!a-z0-9._@+-]*)
		echo "smb: refusing bad login name" >&2
		exit 1
		;;
esac

if id "$name" >/dev/null 2>&1; then
	exit 0
fi

uid="$(awk -F: -v u="$name" '$1==u {print $2; exit}' /data/samba/smbpasswd 2>/dev/null || true)"
case "$uid" in
	""|*[!0-9]*)
		echo "smb: no uid for $name" >&2
		exit 1
		;;
esac
if [ "$uid" -lt 10000 ] || [ "$uid" -gt 59999 ]; then
	echo "smb: uid out of range for $name" >&2
	exit 1
fi

useradd --badname -M -s /bin/false -u "$uid" "$name"
