#!/bin/sh
set -eu

# Validate config before serving; fail fast on typos.
testparm -s /etc/samba/smb.conf > /dev/null

# The app creates users on demand; the file may not exist on first boot.
mkdir -p /data/samba
touch /data/samba/smbpasswd
chmod 600 /data/samba/smbpasswd

exec smbd --foreground --no-process-group --configfile=/etc/samba/smb.conf
