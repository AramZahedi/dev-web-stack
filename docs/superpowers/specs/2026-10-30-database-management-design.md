# Database and Database User Management Design

> **For agentic workers:** This is a design document, not an implementation plan. After the user approves it, the next step is `superpowers:writing-plans`.

**Goal:** Add service-wide database and user management for the installed SQL services, Redis ACL users/password management, optional database creation/linking in Add Site, and a connection-details card on Site Detail.

**Architecture:** The daemon exposes service-scoped database APIs. The privileged agent (WSL on Windows, native on Linux) performs all engine operations through validated, fixed `docker compose exec` commands and streams backups/imports. The agent owns database-user secret files under its service tree; the daemon owns site links, user/backup/job metadata, and audit records. SQLite never stores passwords. The dashboard is a structured control plane, not a SQL console.

**Tech Stack:** Go daemon/helper/agent, SQLite store migrations, Docker Compose services (MySQL 8.4, MariaDB 11.8, PostgreSQL 18, Redis 8.8), Svelte dashboard, Cobra CLI, PowerShell/Go tests.

**Context:** Existing service lifecycle, credential reveal/rotation, config editing, and site env injection are already implemented. Today each SQL service exposes one `devstack` database and one `devstack` user; there is no database/user inventory, grants, links, backups, import/export, or Redis ACL management.

## Intent and why

A developer using devstack needs to create databases for projects, manage application users without dropping to `docker exec`, connect sites to those databases, and move data in and out safely. Databases are service-wide resources: they survive site deletion and can be shared by multiple sites. The dashboard should make the common path easy while keeping destructive operations explicit and auditable.

## Goals

- List, create, and drop databases on installed and active MySQL, MariaDB, and PostgreSQL services; rename databases on PostgreSQL only.
- List, create, delete, password-reset, and grant/revoke engine users, scoped to specific databases.
- Manage Redis ACL users and the Redis password; no Redis logical-database management.
- Link one or more databases to a site, with connection details on Site Detail.
- Offer database creation or attachment in Add Site, with a choice of a new or existing user.
- Back up, restore, export, and import SQL databases without exposing a SQL console.
- Keep all passwords masked until an explicit, audited reveal/copy action.
- Preserve databases and users when a site is deleted.
- Provide CLI parity and SSE refresh events for every mutation.

## Non-goals

- No SQL query editor, ad-hoc SQL execution, or table browser.
- No database management for Redis logical databases (`SELECT` indexes).
- No MySQL/MariaDB database rename in v1; those engines have no safe native rename. The UI offers backup + create new + import instead.
- No scheduled backups, replication, clustering, or remote/public database exposure.
- No database cloning, schema diffing, or migration runner in this version.
- No global/superuser creation for application users.
- No top-level aggregate “all databases” page in v1; management is service-scoped.
- No automatic cleanup of unlinked databases or users.
- No Redis backup/restore/import/export and no Redis site links in v1.

## Anti-goals

- A UI that silently changes grants while attaching an existing user.
- A database or user disappearing because a site was deleted.
- A site silently connecting to a different engine because MySQL and MariaDB share port 3306.
- Passwords in logs, events, list responses, or `state.db`.
- Arbitrary SQL or shell interpolation reaching the container.
- Dropping system databases/users or the devstack admin account.
- A destructive restore that overwrites a database without typed confirmation.

## Constraints

- Database mutations require the service to be installed and active. Inactive services show the inventory as unavailable with an Activate action.
- Backup listing, download, and deletion work while the service is installed but stopped, because they only need the agent filesystem. Backup creation, restore, import, and export require an active service.
- MySQL and MariaDB are mutually exclusive on port 3306; PostgreSQL is independent; Redis is separate.
- Service ports are loopback-only. Site backends run as WSL processes on Windows and native processes on Linux, so `DB_HOST` is `127.0.0.1`, not a container-network alias.
- The agent runs as root inside the managed distro; all engine commands execute inside the service container, never on the host.
- Existing service credentials and the `devstack` database/user are protected.
- Database-user secrets are owned by the agent, never by the daemon or `state.db`. They live under `<agent-root>/services/<service>/secrets/database-users/<user-id>` with 0600 files and a 0700 directory.
- Backups and imports live under the agent’s backups tree, not in the Windows home or `state.db`.
- The dashboard remains loopback-only, session/CSRF protected, and audited.

## Approaches considered

1. **Agent-side engine adapters — chosen.** Extend the agent with per-engine database adapters. Fixed commands run via `docker compose exec`, with SQL and passwords on stdin and dumps/imports streamed. The agent owns secrets; the daemon owns metadata.
   - Pros: matches the privileged-helper boundary; no database passwords in the daemon or SQLite; native dump tools; one pattern for all engines; works on Windows/WSL and Linux.
   - Cons: largest surface; streaming and engine SQL differences need care.
2. **Daemon-side SQL drivers.** The daemon connects to loopback ports with Go drivers.
   - Pros: structured queries and simple metadata reads.
   - Cons: passwords/connections in the daemon process; native backup tools still needed; driver/networking matrix; weaker boundary.
3. **phpMyAdmin or a sidecar admin API.**
   - Pros: less engine code.
   - Cons: MySQL-family only; poor PostgreSQL/Redis parity; unsafe automation surface.

## Domain model

### Database

A database belongs to one SQL service. It has a name, size, table count, creation time (when known), linked sites, and optional devstack metadata (created by devstack, notes). The engine is the source of truth for existence; devstack metadata is reconciled on list.

### User

A user belongs to one engine and can have grants on multiple databases. Application users are ordinary engine users, never the engine admin or devstack admin.

- MySQL/MariaDB: user name plus host scope. Devstack-managed users use `'name'@'%'` inside the loopback-published service; external users may exist as `name@localhost`, `name@10.%`, or several hosts. Every engine user is represented by an opaque devstack user ID, with `username` and `host` stored separately. System users are hidden or read-only.
- PostgreSQL: login role; host is empty.
- Redis: ACL user with on/off state, password, and a simple access level (`read-only` or `full`); no arbitrary ACL rule editor in v1.
- Devstack-managed users get agent-generated passwords stored in the agent secret tree. External users may be linked with a password entered once (stored the same way) or metadata-only with no stored password.
- A user linked to a site cannot be deleted until the links are removed; the UI offers “Unlink sites”.
- Protected users (`root`, `postgres`, `mysql.sys`, `mysql.session`, `mysql.infoschema`, Redis `default`, and the devstack admin user) cannot be deleted or have their admin grants reduced.

### Site link

A site link attaches a site to a database and a user. A database can have many site links; a site can have many database links. One link per site is the primary connection used for `DB_*`/`DATABASE_URL`; additional links use namespaced variables. Deleting a site deletes only its links.

If the linked user has a stored password, the site environment includes the password. If the link is metadata-only, the environment omits `DB_PASSWORD` and `DATABASE_URL`, and Site Detail shows “Password not managed” with a **Set password** action.

### Grants

Database-scoped presets plus advanced toggles:

- **Read-only:** connect/use plus `SELECT` (and view/sequence read where applicable).
- **Read/write:** read-only plus `INSERT`, `UPDATE`, `DELETE` (and sequence usage).
- **Read/write + schema:** read/write plus `CREATE`, `ALTER`, `DROP`, `INDEX`, `REFERENCES` where the engine supports them. This is the default for new site users so framework migrations work.
- **Admin (database-scoped):** all privileges on that database, never global/superuser.
- **Custom:** whitelisted privilege toggles per engine, still database-scoped.

The UI shows the effective preset, individual toggles, and the linked sites. Changing grants applies a diff; it never rewrites global grants. The engine’s live privilege set remains authoritative; devstack persists the intended preset per user/database for display and reconciliation, not as a second source of truth.

### Backups and jobs

- Backups are logical SQL dumps, gzip-compressed, stored at `<agent-root>/backups/databases/<service>/<database>/<timestamp>.sql.gz`.
- Metadata: id, service, database, path, size, SHA-256, kind (`manual`, `pre_drop`, `pre_restore`, `pre_import`), created_at, status.
- No automatic retention deletion in v1; users delete backups explicitly and see disk usage.
- Backup, restore, and import run as background jobs with status/progress; export streams directly.
- Restore defaults to a new database named `<database>_restore_<timestamp>`. Overwriting an existing database means drop-and-recreate the target, requires typing its name, and creates a `pre_restore` backup first.
- Import accepts `.sql` and `.sql.gz`, up to 1 GiB, into an existing database. Two modes: **Merge** (default; pipe into the existing schema) and **Replace contents** (drop-and-recreate the target after a `pre_import` backup). Files are staged under the backups tree with 0600 permissions and deleted after the job.
- On restore/import failure or cancellation, the target may be partially modified. The job is marked failed/canceled, the target is flagged “restore incomplete”, and the pre-operation backup is offered as a one-click recovery. No automatic rollback is attempted.
- Destructive operations require linked sites to be stopped. The confirmation lists them and offers an explicit **Stop linked sites** action; devstack never stops them silently.
- The adapter terminates existing sessions on the target database before drop, overwrite restore, or replace-import (`pg_terminate_backend`; `KILL` for MySQL/MariaDB).
- Redis is excluded from backup/restore/import/export in v1 because it has no managed logical databases.

## Data model

New SQLite migration `0008_databases.sql`:

- `database_links`: id, site_id (FK site, cascade delete), service_id, database_name, user_id (FK database_users, restrict), is_primary, created_at. Unique(site_id, service_id, database_name).
- `database_users`: id (opaque), service_id, username, host, engine, created_at, last_password_rotated_at, password_managed, managed_by_devstack, enabled, access_level (Redis only), last_seen_at. Unique(service_id, username, host). Engine remains authoritative; this table enriches discovery and protects managed users.
- `database_user_grants`: user_id (FK database_users, cascade), database_name, preset, custom_json, updated_at. Primary key(user_id, database_name). This records devstack intent; live engine grants win on conflict and are surfaced in the UI.
- `database_backups`: id, service_id, database_name, path, size_bytes, sha256, kind, status, created_at, error.
- `database_jobs`: id, service_id, database_name, kind (`backup`, `restore`, `import`), status (`queued`, `running`, `succeeded`, `failed`, `canceled`), progress, message, backup_id, created_at, updated_at.
- `audit_events`: id, actor, service_id, site_id, object_type, object_name, action, result, detail_json, created_at. Indexed by created_at, retained to the newest 10,000 rows. The existing `events` table stays a 500-row diagnostic feed; it is not the audit store.

Passwords are not in SQLite. They live under the agent secret tree, namespaced by service and opaque user ID, mode 0600. The agent validates every path component and rejects symlinks and traversal. The daemon never writes or persists these secrets.

## Agent and engine adapters

Add typed agent endpoints (not a generic SQL endpoint):

- `GET/POST /v1/services/{id}/databases`
- `PATCH/DELETE /v1/services/{id}/databases/{name}` (PATCH rename is PostgreSQL-only)
- `GET/POST /v1/services/{id}/users`
- `PATCH/DELETE /v1/services/{id}/users/{userID}`
- `POST /v1/services/{id}/users/{userID}/password` (set or rotate)
- `POST /v1/services/{id}/users/{userID}/reveal`
- `GET/POST /v1/services/{id}/databases/{name}/backups`
- `GET /v1/services/{id}/backups/{backupID}/download`
- `POST /v1/services/{id}/backups/{backupID}/restore`
- `POST /v1/services/{id}/databases/{name}/import`
- `GET /v1/services/{id}/databases/{name}/export`
- Redis user/password routes reuse the user endpoints with an engine adapter.

Each engine adapter implements a common `DatabaseManager` interface for list/create/drop/rename, list/create/drop users, password set/rotate, grants, backup, restore, import, and export. The adapter owns:

- identifier validation and engine quoting;
- fixed command construction with no shell interpolation;
- password generation and agent-side secret writes;
- system object protection;
- engine-specific grant SQL and discovery queries;
- session termination for destructive operations.

Commands run as `docker compose exec -T <service> ...` inside the managed service container. MySQL/MariaDB use `mysql`/`mariadb` and `mysqldump`/`mariadb-dump`; PostgreSQL uses `psql` and `pg_dump`; Redis uses `redis-cli` ACL commands and persists ACL state in `users.acl`.

Engine specifics:

- **MySQL/MariaDB:** identity is `user@host`; devstack-managed users use `%`. Discovery lists all non-system users, including non-`%` hosts, as separate records. Rename is unsupported; the adapter returns a clear engine-limitation error. Drop-user does not drop owned objects.
- **PostgreSQL:** rename uses `ALTER DATABASE ... RENAME TO ...`. Dropping a role that owns objects or has dependent privileges fails by default; the confirmation dialog offers an explicit **Reassign owned objects to the devstack admin** action (`REASSIGN OWNED` per database), then drops the role. Objects are never dropped implicitly.
- **Redis:** `full` means all data commands but excludes admin/dangerous commands (`+@all -@admin -@dangerous` plus explicit exclusions such as `CONFIG`, `DEBUG`, `SHUTDOWN`, `FLUSHALL`, `FLUSHDB`, `ACL`, `MODULE`, `REPLICAOF`, `SLAVEOF`, `MIGRATE`, `SAVE`, `BGSAVE`, `BGREWRITEAOF`, `SWAPDB`, `CLUSTER`, `FAILOVER`). `read-only` uses `+@read` with the same exclusions. Redis users are not site-linkable in v1.

The adapter generates a password with `crypto/rand`, applies the engine change, then writes the secret atomically. If the secret write fails after a successful engine change, the user is marked “password unknown” and the UI offers a reset; the adapter never reports success without a usable secret. Mutations are serialized per service with a mutex. Long-running jobs report progress and are cancellable. The agent streams dumps and imports; it never buffers an entire 1 GiB file in memory.

Redis ACL persistence: on the first Redis user/password change, the adapter backs up the current config, adds `aclfile /data/users.acl`, seeds the ACL file from the live ACL state, and restarts Redis with confirmation. Later changes use `ACL SETUSER` and `ACL SAVE`.

## Daemon and service layer

- `servicecontrol` gains a `DatabaseService` interface backed by the agent adapter, exposed through the existing service manager wiring.
- The daemon owns link metadata, user/backup/job metadata, site orchestration, and audit records. The agent owns password generation, secret storage, reveal, and engine operations.
- The daemon never receives a password for env injection. Site env resolution asks the agent to resolve linked secrets; the daemon only sees a password when a user explicitly reveals it, and that reveal is audited.
- On list, the daemon merges engine-discovered databases/users with SQLite metadata. If an externally dropped database has links, the UI flags it as missing and offers “Remove link”; it never recreates it automatically.
- Site env injection is dynamic: links are resolved at site prepare/terminal time and are not written into `site.Env`. Reserved variables for the primary link: `DB_CONNECTION`, `DB_HOST`, `DB_PORT`, `DB_DATABASE`, `DB_USERNAME`, `DB_PASSWORD`, `DATABASE_URL`. Additional links use `DB_<SLUG>_*`. If the user has set a reserved key, the link value wins and the UI warns that the key is managed. Metadata-only links omit `DB_PASSWORD` and `DATABASE_URL`.
- `DB_HOST` is `127.0.0.1`; site backends are WSL/native processes, not containers on the service network.
- Site start checks every linked service. If the exact linked service is stopped, the site may start with a visible “database offline” warning. If a different member of the same exclusive group is active (for example, the site links MySQL but MariaDB is active on 3306), start is blocked with an actionable error.
- Deactivating or switching a database service warns which sites and backups are affected. It never rewrites links. Uninstall with keep-data preserves databases, users, secrets, and backups; uninstall with delete-data warns and removes links to that service in the same transaction after the data deletion succeeds.
- Password resets and grant changes do not auto-restart sites. Site Detail and the service UI show “Restart linked sites to apply” with an explicit action.
- Site deletion deletes links in the same transaction as the site; database/user/backup rows are untouched.

## HTTP API

Service-scoped routes under `/api/v1/services/{id}`:

- `GET /databases`; `POST /databases`; `GET/PATCH/DELETE /databases/{name}`. `PATCH` rename returns 400 with an engine-limitation message for MySQL/MariaDB.
- `GET/POST /users`; `GET/PATCH/DELETE /users/{userID}`; `POST /users/{userID}/password`; `POST /users/{userID}/reveal`.
- `GET /backups`; `POST /databases/{name}/backups`; `GET /backups/{backupID}/download`; `DELETE /backups/{backupID}`; `POST /backups/{backupID}/restore`.
- `POST /databases/{name}/import` (multipart); `GET /databases/{name}/export` (stream).
- `GET /jobs/{jobID}`; `DELETE /jobs/{jobID}` (cancel).
- Redis: `GET/POST/DELETE /users`, `POST /users/{userID}/password`, `POST /password` for the default user.

Site routes:

- `GET /api/v1/sites/{ref}/databases`
- `POST /api/v1/sites/{ref}/databases` (link)
- `DELETE /api/v1/sites/{ref}/databases/{linkID}` (unlink)

Responses never include passwords. Reveal/password endpoints return the secret once and record an audit event. Destructive requests require `confirm` matching the object name. Long operations return `202` with a job id. Backup list/download/delete are allowed when the service is installed but stopped; backup/restore/import/export return a service-inactive error.

## Add Site flow

The existing three-step wizard stays three steps. Step 2 (Options) gains an optional **Database** panel after domain/options:

- Toggle: “Create or link a database”.
- Choose an installed, active SQL service.
- Choose **Create new database** or **Use existing database**.
- Choose **Create new user** or **Use existing user**.
- New user: agent-generated password, name defaults to a slug of the site name, default grant is read/write + schema on the selected database.
- Existing user: any engine user is selectable. Devstack-managed users use their stored secret. External users can have a password entered once (stored agent-side) or skipped for a metadata-only link. Show current grants on that database. If access is missing, show a warning and a **Manage database user** button; do not auto-grant.
- Review step shows the database, user, grants, host, port, and connection values. It does not show a password because provisioning happens on submit; it shows “Password will be generated when the site is created.”
- After creation, the success state shows the connection card with a one-time reveal/copy action. The same card remains on Site Detail behind the audited reveal.
- If no active SQL service exists, the panel links to Services; it does not block site creation.

Site creation order: validate the database/user selection, then provision the database/user and link, then create the site, then start the site. If site creation fails after provisioning, the database/user remain (service-wide) and the response includes a warning; no automatic drop. If database provisioning fails, no site row is created. No database or user is provisioned during the wizard before final submit.

## Site Detail

Add a **Databases** card between Environment and Terminal:

- One row per linked database: engine, host, port, database, username, primary badge, password state, link to manage.
- Actions: copy host/port/database/username, reveal/copy password when managed, set password when metadata-only, copy `.env` snippet, unlink.
- Shared users are labeled “shared user”; the card links to the user’s canonical record.
- A “Link database” action opens the same chooser as Add Site.
- Passwords are masked by default and revealed only through the audited user credential endpoint.
- If the linked service is stopped, show “Database offline” with an Activate action. If a conflicting exclusive-group engine is active, show the blocking error.
- After a password reset or grant change, show “Restart site to apply” with an explicit restart action.

## Dashboard service management

Add a service detail route `#/services/:id` with tabs:

- **Databases:** table/cards with name, size, tables, linked sites, created; actions create, rename (PostgreSQL only), backup, export, import, restore, drop.
- **Users:** username, host/engine, enabled, password state, grants summary, linked sites; actions create, edit grants, set/reset password, reveal, delete.
- **Backups:** database, timestamp, size, checksum, kind, status; actions download, restore, delete; import/export entry points. List/download/delete remain available when the service is stopped.
- **Redis:** Users and Password tabs only.

Service cards get a **Manage databases** button for installed SQL services and **Manage users** for Redis. The general Services overview stays high-level and does not expose secrets.

Confirmation rules:

- Drop database: type the database name; “Back up first” checked by default; linked sites must be stopped first.
- Drop user: type the username; blocked while linked, with an Unlink action. PostgreSQL offers explicit reassignment of owned objects; it never drops them implicitly.
- Restore overwrite: type the target database name; pre-restore backup required; linked sites stopped.
- Import: confirm target and Merge vs Replace contents; offer a pre-import backup; linked sites stopped for Replace.
- Rename: PostgreSQL only; type the new name; linked sites stopped.
- All destructive actions show what will and will not be deleted.

## CLI

New `devstack db` group:

- `db list`, `db create`, `db rename` (PostgreSQL only), `db drop`
- `db user list/create/delete`, `db user password`, `db user grant`
- `db backup list/create/download`, `db backup restore`, `db export`, `db import`
- `db link`, `db unlink`
- Redis: `db user ...`, `db password`

`site add` gains `--database-service`, `--database`, `--database-new`, `--database-user`, `--database-user-new`, `--database-user-password`, `--database-grant`. `site db list/link/unlink` mirrors the API. Mutating commands require `--yes`; JSON output never includes passwords except explicit `--reveal` actions. `db user password --set` supports external users; omitting it creates a metadata-only link.

## Security and safety

- Identifier regex per engine; reject quotes, dots where unsafe, control characters, path separators, and reserved names. MySQL/MariaDB host scopes are validated separately.
- System objects are immutable: `mysql`, `information_schema`, `performance_schema`, `sys`, `postgres`, `template0`, `template1`, `devstack`, `root`, `postgres`, `mysql.sys`, `mysql.session`, `mysql.infoschema`, and Redis `default` cannot be dropped or have their admin grants reduced.
- No arbitrary SQL; custom grants map to a whitelist.
- Passwords are generated by the agent, stored only in the agent secret tree, and never appear in argv, daemon memory beyond a reveal response, logs, events, list responses, or SQLite. Use stdin or container secret files.
- External-user passwords are optional; a metadata-only link never invents or stores a password.
- Backup/import paths are confined under the agent backups tree; reject symlinks, `..`, and absolute traversal. Files are 0600 and deleted after import.
- Import size limit 1 GiB; backup/import jobs are cancellable and serialized per service.
- Destructive operations stop linked sites only after explicit confirmation and terminate target-database sessions.
- Every mutation and reveal emits a durable `audit_events` row with service, object, action, success, and no secret values.
- Service uninstall with `--delete-data` warns that databases, users, backups, and links will be deleted; keep-data preserves them. Delete-data removes links only after the data deletion succeeds.
- Redis ACL file migration backs up the config and restarts the service once with confirmation.

## Events and observability

Emit SSE events: `database.created`, `database.renamed`, `database.dropped`, `database.user.created`, `database.user.deleted`, `database.user.password_changed`, `database.user.grants_changed`, `database.backup.created`, `database.backup.restored`, `database.backup.deleted`, `database.import.completed`, `database.export.started`, `database.link.changed`, `redis.user.*`, `redis.password.changed`, `database.job.updated`. The dashboard refreshes the relevant service/site views and shows job progress. Audit rows are durable even when the diagnostic event feed is pruned.

## Testing

- Unit: identifier and host-scope validation, grant SQL mapping, system-object protection, agent secret-store path safety, link cascade, Add Site orchestration, env injection (managed vs metadata-only), backup metadata, job state machine, audit retention.
- Engine integration: real MySQL/MariaDB/PostgreSQL containers for create/drop DB, PostgreSQL rename, user create/delete, password set/rotate, presets/custom grants, backup/restore/import/export round-trip, failure recovery, concurrent mutation locking, session termination.
- Redis integration: ACL user create/delete/password/enable, `users.acl` persistence across restart, default-user password, admin-command exclusion for `full`.
- Security: injection attempts through names/grants/hosts, traversal in backup/import paths, unauthorized reveal, CSRF, deleting protected objects, wrong-engine site start.
- UI: AddSite database panel (new/existing DB, new/existing user, optional external password, missing-grant warning), Site Detail credentials card, service tabs, typed confirmations, masked passwords, stopped-service backup access, 0 console errors.
- Live E2E: install/activate each service, create DB/user, link a site, verify the app receives `DB_*`/`DATABASE_URL`, delete the site and prove the DB remains, backup/restore/import/export, service switch/deactivate behavior, uninstall cleanup.
- CLI: every command with `--json`, confirmation, error paths, and parity with the dashboard.

## Rollout and compatibility

- Migration adds empty link/user/grant/backup/job/audit tables; existing services are untouched.
- Existing `devstack` database/user and admin credentials remain protected and visible in the service credential dialog.
- Redis gains an `aclfile` config only when the user first manages users/passwords; the config is backed up, existing data is preserved, and the service restarts once with confirmation.
- No site row or env changes are required for existing sites; links are additive.
- Database/user discovery works for objects created outside devstack, marked as unmanaged; they are manageable except protected system objects, and destructive actions still require confirmation.
- MySQL/MariaDB rename is not offered; the UI explains the engine limitation and offers backup + create new + import.
- Uninstall keep-data preserves the service tree (data, secrets, backups); backups are listed again after reinstall. Uninstall delete-data removes links after deleting the data.

## Decisions from the conversation

- Databases are service-wide, not site-owned.
- Site deletion never touches databases, users, grants, passwords, or backups.
- No SQL execution in the dashboard.
- Redis has user/password management only, no logical database management.
- Add Site offers create-vs-existing database and create-vs-existing user.
- An existing user without access gets a warning and a “Manage database user” button; no automatic grant.
- Backup/restore and SQL import/export are in the first version.
- Site Detail is the primary connection-details view; service management is the canonical inventory.
- Passwords are masked with explicit reveal/copy and audit.
- Any existing engine user can be selected; passwords are optional and stored agent-side when provided.

## Resolved review questions

- **MySQL/MariaDB rename:** not supported; PostgreSQL-only rename. No metadata-only fake rename.
- **Secret location:** agent-owned service tree, not the Windows daemon. The daemon sees a secret only for an explicit reveal.
- **External users:** selectable; password optional; metadata-only links omit password env values.
- **Service switch/deactivate:** links stay; wrong exclusive-group engine blocks site start; delete-data removes links after success.
- **Restore/import overwrite:** drop-and-recreate with typed confirmation and a pre-operation backup; failures are flagged with one-click recovery.
- **Linked sites/sessions:** destructive operations require stopped sites and terminate target sessions; password/grant changes prompt a restart.
- **MySQL host scopes:** `username` + `host` columns and opaque user IDs; non-`%` users are separate records.
- **Add Site preview:** no provisioning before submit; the review shows connection values, and the password appears after creation.
- **PostgreSQL role deletion:** fails by default; explicit reassign-owned-objects action, never implicit object drops.
- **Grant state:** per-user/per-database table for intent; engine privileges are authoritative.
- **Redis:** `full` excludes admin/dangerous commands; Redis users are not site-linkable in v1.
- **Audit:** durable `audit_events` table, not the 500-row diagnostic feed.
- **Stopped service:** backup list/download/delete work; backup/restore/import/export require active.

## What is left to the builder

- Exact Svelte layout/components and copy within the service tabs and Site Detail card.
- Exact CLI flag spelling beyond the required operations.
- Job progress granularity and polling interval (SSE plus fallback polling).
- Backup filename/timestamp format and download naming.
- Grant preset naming per engine and the exact custom privilege list.
- UI behavior when a linked database is missing externally (flag + unlink, no auto-recreate).
- Exact wording of engine-limitation and wrong-engine errors.
