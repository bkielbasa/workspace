# Server Setup

After installing the application, complete the following steps to make the server fully functional and compatible with mail and Apple clients.

## 1. DNS

- A record: point your domain (e.g. `mail.example.com`) to your server IP
- MX record:
  - `example.com → mail.example.com`

### SPF

```
v=spf1 mx ~all
```

### DKIM

- Generate key:
```
openssl genrsa -out dkim_private.key 2048
openssl rsa -in dkim_private.key -pubout -out dkim_public.key
```

- Add DNS record:
```
default._domainkey.example.com

v=DKIM1; k=rsa; p=<PUBLIC_KEY>
```

### DMARC

```
_dmarc.example.com

v=DMARC1; p=none; rua=mailto:postmaster@example.com
```

## 2. TLS

- Obtain certificate (e.g. Let's Encrypt)
- Set environment variables:

```
TLS_CERT_FILE=/path/to/fullchain.pem
TLS_KEY_FILE=/path/to/privkey.pem
```

## 3. DKIM config

```
DKIM_DOMAIN=example.com
DKIM_SELECTOR=default
DKIM_PRIVATE_KEY_FILE=/path/to/dkim_private.key
```

## 4. Database

- Start your SQL database
- Set connection in config (`loadConfig()`)
- Run migrations (external tool/container)

## 5. Run server

```
go run main.go
```

Services started:
- HTTPS: 443
- IMAPS: 993
- SMTPS: 465

## 6. Apple / client auto-setup

Ensure these endpoints are reachable over HTTPS:

- `/.well-known/autoconfig/mail/config-v1.1.xml`
- `/.well-known/carddav`
- `/.well-known/caldav`

## 7. Test

- Add account in Apple Mail / iOS
- Verify:
  - Mail works (IMAP/SMTP)
  - Contacts appear
  - Calendars appear

## Notes

- Use valid TLS certificates (self-signed may fail auto-setup)
- Domain should match email addresses
- Ports 443 / 993 / 465 are recommended for compatibility

## Let's Encrypt (automation)

### Option A: Caddy (simplest)

`Caddyfile`:

```
mail.example.com {
  reverse_proxy localhost:443
}
```

Run Caddy (it will obtain and renew certificates automatically):

```
caddy run --config Caddyfile
```

Notes:
- Point your app to listen on an internal port (e.g. 8443) and proxy from Caddy
- Or keep app on 443 and let Caddy terminate TLS and proxy to another port

### Option B: Traefik (Docker)

Minimal example labels:

```
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.mail.rule=Host(`mail.example.com`)"
  - "traefik.http.routers.mail.entrypoints=websecure"
  - "traefik.http.routers.mail.tls.certresolver=letsencrypt"
  - "traefik.http.services.mail.loadbalancer.server.port=443"
```

Traefik will:
- obtain certs via ACME
- renew automatically

Ensure ports 80/443 are open for HTTP-01 challenge.

## Troubleshooting

### 1. Mail connects but no folders (Sent/Trash/etc.)

- Check IMAP `LIST` response includes flags:
  - `\\Sent`, `\\Trash`, `\\Drafts`, `\\Junk`
- Ensure default mailboxes exist (auto-created on login)

### 2. Contacts/Calendars not appearing

- Verify endpoints over HTTPS:
  - `/.well-known/carddav`
  - `/.well-known/caldav`
  - `/.well-known/autoconfig/mail/config-v1.1.xml`
- Check PROPFIND on `/dav/` and `/cal/` returns:
  - `current-user-principal`
  - `addressbook-home-set` / `calendar-home-set`
- Ensure Basic Auth works for DAV

### 3. Apple account added but no auto-detection

- Domain mismatch (email vs hostname)
- Invalid TLS cert (not trusted)
- Missing autoconfig endpoint

### 4. Emails land in spam

- Check DNS:
  - SPF present
  - DKIM valid (use Gmail "Show original")
  - DMARC present
- Ensure PTR (reverse DNS) matches your domain

### 5. DKIM not applied

- Verify env vars:
  - `DKIM_DOMAIN`
  - `DKIM_SELECTOR`
  - `DKIM_PRIVATE_KEY_FILE`
- Check logs for: `dkim enabled`

### 6. IMAP/SMTP connection issues

- Ports open: 993, 465
- Firewall rules
- TLS cert paths correct

### 7. Autoconfig not working

- Open in browser:
  - `https://mail.example.com/.well-known/autoconfig/mail/config-v1.1.xml`
- Must return valid XML (no redirects to HTTP)
