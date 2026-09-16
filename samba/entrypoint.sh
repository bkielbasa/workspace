#!/bin/sh
set -eu

# Validate config before serving; fail fast on typos.
testparm -s /etc/samba/smb.conf > /dev/null

# The app creates users on demand; the file may not exist on first boot.
mkdir -p /data/samba
touch /data/samba/smbpasswd
chmod 600 /data/samba/smbpasswd

# Every passdb login needs a matching UNIX account (locked shell, no home).
# New users appear at runtime; per-connect provisioning (root preexec below)
# covers those, this covers everything already on disk.
cut -d: -f1 /data/samba/smbpasswd | while IFS= read -r login; do
	[ -n "$login" ] && /scripts/ensure-user.sh "$login" || true
done

exec smbd --foreground --no-process-group --configfile=/etc/samba/smb.conf
