# Workspace

**Workspace** is a self-hosted personal communication and productivity suite written in Go. It combines a dark-themed, server-rendered web application with standard mail and synchronization protocols: a Gmail-inspired webmail client, an interactive calendar with drag-and-drop rescheduling, a contacts address book, account profile management, and native SMTP, IMAP, CalDAV, and CardDAV servers.

---

## Key Features

### 1. Web Application (Server-Rendered + HTMX)
- **Mail (Gmail-inspired)**:
  - Mailbox folders (`Inbox`, `Sent`, `Drafts`, `Trash`, `Spam`) with unread count badges.
  - Search, star/unstar (`★`), mark read/unread, and deletion/trashing.
  - Detailed message view with parsed RFC 5322 MIME headers and body formatting.
  - Floating compose dock with keyboard shortcuts (`c` to compose, `Esc` to close) and reply support.
- **Calendar**:
  - Weekly view with time grid.
  - Drag-and-drop event rescheduling.
  - Double-click to edit events, plus event creation and deletion.
  - CalDAV synchronization.
- **Contacts**:
  - Address book with search, full contact editing (names, company, title, structured emails, phone numbers, addresses, and notes).
- **User Profile & Security**:
  - Account details view (email, creation date).
  - Display name editing.
  - In-app password change with Argon2id hashing and automatic session revocation/rotation.
- **Authentication**:
  - Cookie sessions (SHA-256 hashed tokens, 24-hour TTL, background cleanup).
  - CSRF protection via double-submit cookies (`X-CSRF-Token` header / `_csrf` form field).
  - Rate-limited sign-in attempts.

### 2. Mail Services (SMTP & IMAP)
- **SMTP Server**: Port `2525` (plain) and `2465` (SMTPS with TLS) for receiving and routing mail.
- **IMAP Server**: Port `1143` (plain) and `1993` (IMAPS with TLS) for desktop/mobile email clients.
- **Delivery Worker**: Background worker handling queued outbound email delivery.

### 3. Sync & Client Autodiscovery
- **CalDAV** (`/cal/`, `/.well-known/caldav`): Calendar syncing for Apple Calendar, Thunderbird, etc.
- **CardDAV** (`/dav/`, `/.well-known/carddav`): Address book syncing.
- **Client Autodiscovery**:
  - Mozilla Thunderbird Autoconfig (`/mail/config-v1.1.xml`)
  - Microsoft Outlook Autodiscover (`/autodiscover/autodiscover.xml`)
  - Apple Mobileconfig profile generator (`/apple.mobileconfig`)

### 4. REST API & Observability
- HTTP REST API for user administration (`/users`), contacts, and message threads.
- OpenTelemetry tracing and HTTP metrics.

---

## Getting Started

### Prerequisites
- **Go** 1.22+
- **PostgreSQL** database

### Running
```bash
go run main.go
```

On startup, the server connects to PostgreSQL (with retry logic), initializes the database services, and launches SMTP, IMAP, HTTP/Web UI, and the outbound delivery worker.

---

## Configuration

Configured via environment variables:

| Variable | Description | Default |
|---|---|---|
| `DATABASE_URL` | PostgreSQL connection URL | required |
| `HTTP_ADDR` | Web UI and HTTP API listen address | `:8080` |
| `MAIL_HOSTNAME` | Hostname for SMTP/IMAP HELO and autodiscovery | `localhost` |
| `TLS_CERT_FILE` | Path to TLS certificate file (enables SMTPS & IMAPS) | _optional_ |
| `TLS_KEY_FILE` | Path to TLS private key file | _optional_ |
| `COOKIE_SECURE` | Force `Secure` flag on session and CSRF cookies (`true`/`false`) | `false` |

---

## Key Routes Overview

### Web UI
- `GET /login`, `POST /login` – Sign in
- `POST /logout` – Sign out
- `GET /` – Home dashboard
- `GET /mail` – Webmail client
- `GET /mail/message/{id}` – Message view
- `POST /mail/send` – Compose and send email
- `POST /mail/message/{id}/toggle-star` – Star / unstar message
- `POST /mail/message/{id}/toggle-read` – Mark read / unread
- `POST /mail/message/{id}/delete` – Delete message
- `GET /calendars`, `POST /calendars`, `POST /calendars/{id}`, `DELETE /calendars/{id}` – Calendar
- `GET /contacts`, `POST /contacts`, `GET /contacts/{id}`, `POST /contacts/{id}`, `DELETE /contacts/{id}` – Contacts
- `GET /profile`, `POST /profile`, `POST /profile/password` – Profile & password settings

### Protocols & Discovery
- `Port 2525` / `2465` – SMTP / SMTPS
- `Port 1143` / `1993` – IMAP / IMAPS
- `/cal/` – CalDAV
- `/dav/` – CardDAV
- `/mail/config-v1.1.xml` – Thunderbird autoconfig
- `/autodiscover/autodiscover.xml` – Outlook Autodiscover
- `/apple.mobileconfig` – Apple device configuration profile
