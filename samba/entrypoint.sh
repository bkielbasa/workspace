#!/bin/sh
set -eu

# Validate config before serving; fail fast on typos.
testparm -s /etc/samba/smb.conf > /dev/null

# The app creates users on demand; the file may not exist on first boot.
mkdir -p /data/samba
touch /data/samba/smbpasswd
chmod 600 /data/samba/smbpasswd

# Every passdb login needs a matching UNIX account (locked shell, no home).
# Users appear at runtime (written by the app on another mount client, so
# inotify would never fire here): ensure once at boot, then re-ensure on a
# timer. New logins become usable within one interval, no restarts, no
# reloads, no coordination channel.
ensure_all_users() {
	cut -d: -f1 /data/samba/smbpasswd 2>/dev/null | while IFS= read -r login; do
		[ -n "$login" ] && /scripts/ensure-user.sh "$login" || true
	done
}

ensure_all_users
(while sleep 30; do ensure_all_users; done) &

exec smbd --foreground --no-process-group --configfile=/etc/samba/smb.conf
