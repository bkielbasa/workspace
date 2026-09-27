# Workspace

**Workspace** is a self-hosted, private communication and productivity platform that gives you complete ownership of your personal, family, and team data. It serves as an all-in-one alternative to proprietary cloud ecosystems (such as Google Workspace, Microsoft 365, and Apple iCloud)—unifying email, calendars, contacts, notes, checklists, and file storage in a single cohesive dashboard.

---

## Why Workspace?

- **Data Sovereignty & Privacy**: Your messages, schedules, contacts, and personal notes stay entirely in your control. No third-party data mining, ad targeting, or recurring per-seat subscription fees.
- **Unified Productivity Hub**: Eliminates tool fragmentation by bringing everyday personal, household, and team organization tools into one clean, responsive interface.
- **Real-Time Collaboration**: Share household checklists, grocery lists, and notes with family or team members with instant multi-device synchronization.
- **Universal Ecosystem Compatibility**: Works out of the box with the native client apps you already use on iOS, macOS, Android, Windows, and Linux via standard protocols (IMAP, SMTP, CalDAV, CardDAV).
- **Zero-Friction Device Onboarding**: Generate ready-to-install configuration profiles for Apple devices and automatic discovery for desktop clients to get connected in seconds without manual setup headaches.
- **Lightweight & Low Maintenance**: Designed for minimal operational overhead, delivering fast performance and high reliability from a single lightweight service backed by PostgreSQL.

---

## Core Capabilities

### 📬 Mail & Communications
- Responsive webmail client with message search, folder management, and quick keyboard navigation.
- Personalized email signatures and automated inbox filtering rules to keep correspondence organized.
- Standard mail protocol support (SMTP & IMAP) for Apple Mail, Thunderbird, Outlook, and mobile clients.

### 📅 Calendar & Scheduling
- Visual weekly calendar planner with intuitive drag-and-drop rescheduling.
- Seamless bi-directional synchronization with Apple Calendar, Thunderbird, and mobile devices via CalDAV.

### 📝 Notes & Checklists
- Visual card dashboard with color labeling, tagging, and pinned notes for quick access.
- Interactive checklists with real-time updates and family sharing for grocery lists and household tasks.
- Native task synchronization with Apple Reminders and standard task managers via CalDAV.

### 👥 Contacts & Address Book
- Centralized address book keeping personal and professional contacts organized.
- Continuous CardDAV synchronization across all connected smartphones, tablets, and computers.

### 📁 Files & Storage
- Integrated network file sharing (Samba/SMB) for convenient document and media access across your local network.

### ⚙️ Centralized Settings & Security
- Unified settings hub to easily manage personal profiles, account security, email signatures, filtering rules, and domain configuration in one place.

---

## Quick Start

### Running with Docker Compose
```bash
docker compose up -d
```

### Manual Setup
**Prerequisites:** Go 1.22+ and PostgreSQL.

```bash
# Configure database connection
export DATABASE_URL="postgres://user:password@localhost:5432/workspace?sslmode=disable"

# Start the application
go run main.go
```

The web dashboard will be available at `http://localhost:8080`.

---

## Configuration

Workspace is configured using environment variables:

| Variable | Purpose | Default |
|---|---|---|
| `DATABASE_URL` | PostgreSQL connection string | *Required* |
| `HTTP_ADDR` | Web interface and HTTP API address | `:8080` |
| `MAIL_HOSTNAME` | Hostname used for mail services and autodiscovery | `localhost` |
| `TLS_CERT_FILE` | Path to TLS certificate for secure connections | *Optional* |
| `TLS_KEY_FILE` | Path to TLS private key | *Optional* |
| `COOKIE_SECURE` | Enforce secure session cookies over HTTPS | `false` |
