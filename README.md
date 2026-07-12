# Mail Server (SMTP / IMAP / HTTP API)

This project is a lightweight mail system written in Go. It provides SMTP for sending mail, IMAP for retrieving mail, an HTTP API for user and contact management, and basic observability (metrics + tracing).

## Features

- SMTP server (port 2525) for receiving email
- SMTPS (port 2465) when TLS is configured
- IMAP server (port 1143) for mailbox access
- IMAPS (port 1993) when TLS is configured
- HTTP API for users, contacts, and threads
- CardDAV and CalDAV endpoints (basic support)
- Background worker for outbound mail delivery
- Automatic demo data seeding on startup
- OpenTelemetry tracing + basic HTTP metrics
- Retry logic for database connection (useful for containers)

## Getting Started

### Requirements

- Go
- A running SQL database

### Run

```
go run main.go
```

The server will:
- connect to the database
- seed demo data
- start SMTP, IMAP, HTTP servers, and worker

## Usage

### SMTP

- Connect to `localhost:2525`
- Send email using any SMTP client

### IMAP

- Connect to `localhost:1143`
- Use credentials created via API or seeded data

### HTTP API

Base address: `http://localhost:<httpAddr>`

User endpoints:
- `POST /users` – create user
- `GET /users` – list users
- `GET /users/{id}` – get user
- `PATCH /users/{id}` – update user
- `DELETE /users/{id}` – delete user
- `POST /users/{id}/password` – change password

Contacts:
- `GET /contacts`
- `POST /contacts`
- `DELETE /contacts`

Threads:
- `GET /threads`

DAV endpoints:
- `/dav/` – CardDAV
- `/cal/` – CalDAV

## Configuration

Environment variables:

- `TLS_CERT_FILE` – path to TLS certificate
- `TLS_KEY_FILE` – path to TLS key

If not provided, TLS servers (SMTPS/IMAPS) are disabled.

Database connection is configured via `loadConfig()` (see code).

## Development

- Entry point: `main.go`
- HTTP routes defined in `http.ServeMux`
- Core services:
  - Users
  - Mail
  - Mailboxes
  - Contacts
  - Threads
  - Delivery / Worker

Observability:
- Tracing initialized at startup
- HTTP requests wrapped with OpenTelemetry middleware

## Notes

- Database migrations are expected to be handled externally (e.g. a separate container)
- Demo data is seeded automatically on startup
- Worker currently has DKIM disabled (placeholder in code)

## License

Add your license here.
