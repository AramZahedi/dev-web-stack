# Dashboard Service Database Management Lane Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task in an isolated worktree. Steps use checkbox (`- [ ]`) syntax for tracking. Do not merge; the controller merges after audit.

**Goal:** Add a `/services/:id` detail route with Databases, Users, Backups, and Redis tabs that manage the service-wide inventory through the frozen database API contract.

**Architecture:** A new `ServiceDetail.svelte` route loads the service and its inventory, then renders focused tab components. All secret reveal, typed confirmation, connection display, and API calls go through the frozen foundation modules and existing shared components. This lane edits no Go code and no foundation-owned file.

**Tech Stack:** Svelte 5 runes, TypeScript, Vitest, Testing Library, existing `Card`/`Modal`/`ConfirmDialog`/`Button`/`Badge`/`EmptyState`/`ErrorBanner` components.

**Spec:** `docs/superpowers/specs/2026-10-30-database-management-design.md`

**Foundation:** `docs/superpowers/plans/2026-10-30-database-management-00-foundation.md`

**Base:** the frozen foundation SHA recorded in `.superpowers/db-foundation-sha.txt`.

## Global Constraints

- Databases are service-wide; this lane never implies a database belongs to a site.
- No SQL console, query editor, or arbitrary SQL input.
- MySQL/MariaDB database rename is hidden; PostgreSQL rename is shown.
- Passwords are masked until an explicit reveal; reveal calls the audited API and never renders a secret from list data.
- Destructive actions use the existing `ConfirmDialog` with `requireText` matching the object name.
- Backup list/download/delete work when the service is installed but stopped; backup/restore/import/export require active.
- Redis has Users and Password tabs only; no Databases, Backups, import, or export.
- The service overview (`/services`) stays high-level; secrets live only in the detail route.
- All existing UI tests must stay green.

## Review Focus

- A masked password must not be present in the DOM before the explicit reveal call returns.
- A typed confirmation must stay disabled until the exact database or user name is entered.
- MySQL/MariaDB must not offer a rename action; PostgreSQL must.
- A stopped service must still list and download backups, but backup/restore/import/export must show an inactive-service error.
- Redis must not render database or backup tabs, and `full` must not be described as superuser access.

## Files Owned

- Create: `ui/src/routes/ServiceDetail.svelte`
- Create: `ui/src/routes/ServiceDetail.test.ts`
- Create: `ui/src/lib/components/service/DatabaseTab.svelte`
- Create: `ui/src/lib/components/service/UsersTab.svelte`
- Create: `ui/src/lib/components/service/BackupsTab.svelte`
- Create: `ui/src/lib/components/service/RedisTab.svelte`
- Create: `ui/src/lib/components/service/GrantEditor.svelte`
- Create: `ui/src/lib/components/service/DatabaseFormDialog.svelte`
- Create: `ui/src/lib/components/service/UserFormDialog.svelte`
- Modify: `ui/src/App.svelte` (add `/services/:id` route only)
- Test: component tests colocated as `*.test.ts`

Do not edit `ui/src/lib/databaseTypes.ts`, `databaseApi.ts`, `databaseMock.ts`, `SecretField.svelte`, `DatabaseConnectionCard.svelte`, `types.ts`, `api.ts`, `mock.ts`, or any Go file. Request a foundation amendment if a contract is missing.

## Interfaces

Consumes from foundation:

```ts
// ui/src/lib/databaseTypes.ts
Database, DatabaseUser, DatabaseGrant, DatabaseBackup, DatabaseJob,
DatabaseConnection, DatabaseEngine, GrantPreset

// ui/src/lib/databaseApi.ts
listServiceDatabases(id): Promise<{ databases: Database[] }>
createServiceDatabase(id, body): Promise<Database>
getServiceDatabase(id, name): Promise<Database>
renameServiceDatabase(id, name, body): Promise<Database>
dropServiceDatabase(id, name, body): Promise<void>
listServiceUsers(id): Promise<{ users: DatabaseUser[] }>
createServiceUser(id, body): Promise<DatabaseUser>
updateServiceUser(id, userID, body): Promise<DatabaseUser>
deleteServiceUser(id, userID, body): Promise<void>
setServiceUserPassword(id, userID): Promise<Secret>
revealServiceUserPassword(id, userID): Promise<Secret>
listServiceBackups(id): Promise<{ backups: DatabaseBackup[] }>
createServiceBackup(id, database, body): Promise<DatabaseJob>
deleteServiceBackup(id, backupID): Promise<void>
restoreServiceBackup(id, backupID, body): Promise<DatabaseJob>
downloadServiceBackup(id, backupID): Promise<Blob>
importServiceDatabase(id, database, file, body): Promise<DatabaseJob>
exportServiceDatabase(id, database): Promise<Blob>
getDatabaseJob(id): Promise<DatabaseJob>
cancelDatabaseJob(id): Promise<void>
```

Shared components: `SecretField.svelte` (`value`, `revealed`, `loading`, `onreveal`, `onhide`), `DatabaseConnectionCard.svelte` (`connection`, `title`, `showEnv`).

Existing: `Card`, `Modal`, `ConfirmDialog`, `Button`, `Badge`, `EmptyState`, `ErrorBanner`, `Field`, `Toggle`, `Skeleton`.

Produces for the controller: `/services/:id` route and the four tab components. No other lane imports this lane's files.

---

### Task 1: Route shell, service load, and tab selection

**Files:**
- Create: `ui/src/routes/ServiceDetail.svelte`
- Modify: `ui/src/App.svelte:16-26`
- Test: `ui/src/routes/ServiceDetail.test.ts`

**Interfaces:**
- Consumes: `getService(id)` from `api.ts`; `listServiceDatabases`, `listServiceUsers`, `listServiceBackups` from `databaseApi.ts`.
- Produces: `ServiceDetail` component with `params: { id?: string }`; tabs `databases | users | backups | password`; Redis uses `users | password` only.

- [ ] **Step 1: Write the failing route test**

```ts
it('renders service detail with database tabs', async () => {
  const api = await import('../lib/api');
  const db = await import('../lib/databaseApi');
  vi.mocked(api.getService).mockResolvedValue(MYSQL_SERVICE);
  vi.mocked(db.listServiceDatabases).mockResolvedValue({ databases: MYSQL_DATABASES });
  vi.mocked(db.listServiceUsers).mockResolvedValue({ users: MYSQL_USERS });
  vi.mocked(db.listServiceBackups).mockResolvedValue({ backups: MYSQL_BACKUPS });
  render(ServiceDetail, { props: { params: { id: 'mysql' } } });
  expect(await screen.findByRole('heading', { name: 'MySQL 8.4' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Databases' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Users' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Backups' })).toBeInTheDocument();
});

it('renders Redis with users and password only', async () => {
  vi.mocked(api.getService).mockResolvedValue(REDIS_SERVICE);
  render(ServiceDetail, { props: { params: { id: 'redis' } } });
  expect(await screen.findByRole('tab', { name: 'Users' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Password' })).toBeInTheDocument();
  expect(screen.queryByRole('tab', { name: 'Databases' })).not.toBeInTheDocument();
  expect(screen.queryByRole('tab', { name: 'Backups' })).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd ui; npx vitest run src/routes/ServiceDetail.test.ts`
Expected: FAIL, module `ServiceDetail.svelte` not found.

- [ ] **Step 3: Implement the route shell**

Add `import ServiceDetail from './routes/ServiceDetail.svelte';` and `'/services/:id': ServiceDetail` to `routes` in `App.svelte`. In `ServiceDetail.svelte`, read `params.id`, load `getService`, then the three inventory calls with independent catches so one failed tab does not blank the page. Parse `tab` and `user` query parameters from `window.location.hash` (for example `#/services/mysql?tab=users&user=usr_app`) and preselect the matching tab/user; this is the landing target for the Add Site and Site Detail “Manage database user” links. Use a generation counter like `SiteDetail.svelte:285-360`, a `devstack:refresh` listener, and a transient poll only while a job is running. Render a header with service name, state badge, Activate/Deactivate/Restart actions, and a tablist. Default tab: `databases` for SQL services, `users` for Redis.

- [ ] **Step 4: Re-run the test**

Run: `cd ui; npx vitest run src/routes/ServiceDetail.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add ui/src/routes/ServiceDetail.svelte ui/src/routes/ServiceDetail.test.ts ui/src/App.svelte
git commit -m "feat(ui): add service detail route with database tabs"
```

---

### Task 2: Databases tab

**Files:**
- Create: `ui/src/lib/components/service/DatabaseTab.svelte`
- Create: `ui/src/lib/components/service/DatabaseFormDialog.svelte`
- Test: `ui/src/lib/components/service/DatabaseTab.test.ts`

**Interfaces:**
- Consumes: database list/create/rename/drop/backup/import/export functions from `databaseApi.ts`; `SecretField`, `DatabaseConnectionCard`, `ConfirmDialog`.
- Produces: `DatabaseTab` with props `{ service: ServiceView; databases: Database[]; onRefresh: () => void }`.

- [ ] **Step 1: Write the failing tests**

```ts
it('lists databases with size, tables, and linked sites', () => {
  render(DatabaseTab, { props: { service: MYSQL_SERVICE, databases: MYSQL_DATABASES, onRefresh: vi.fn() } });
  expect(screen.getByText('app')).toBeInTheDocument();
  expect(screen.getByText('12.4 MB')).toBeInTheDocument();
  expect(screen.getByText('blog')).toBeInTheDocument();
});

it('hides rename for mysql and shows it for postgres', () => {
  const { rerender } = render(DatabaseTab, { props: { service: MYSQL_SERVICE, databases: MYSQL_DATABASES, onRefresh: vi.fn() } });
  expect(screen.queryByRole('button', { name: /rename app/i })).not.toBeInTheDocument();
  rerender({ service: POSTGRES_SERVICE, databases: POSTGRES_DATABASES, onRefresh: vi.fn() });
  expect(screen.getByRole('button', { name: /rename app/i })).toBeInTheDocument();
});

it('requires the exact database name before dropping', async () => {
  render(DatabaseTab, { props: { service: MYSQL_SERVICE, databases: MYSQL_DATABASES, onRefresh: vi.fn() } });
  await user.click(screen.getByRole('button', { name: /drop app/i }));
  const confirm = screen.getByRole('button', { name: /drop database/i });
  expect(confirm).toBeDisabled();
  await user.type(screen.getByLabelText(/type “app” to confirm/i), 'app');
  expect(confirm).toBeEnabled();
});
```

- [ ] **Step 2: Run and watch fail**

Run: `cd ui; npx vitest run src/lib/components/service/DatabaseTab.test.ts`
Expected: FAIL, component not found.

- [ ] **Step 3: Implement the tab**

Render a table/card list with name, size, table count, linked sites, and actions: Create database, Rename (PostgreSQL only), Backup, Export, Import, Restore (from the Backups tab), Drop. Use `DatabaseFormDialog` for create/rename. Use `ConfirmDialog` with `requireText={database.name}` for drop; include a checked-by-default “Back up first” toggle and the linked-sites warning. On success, call `onRefresh` and emit a toast. For import, use a hidden file input accepting `.sql,.sql.gz`; call `importServiceDatabase` with `mode: 'merge' | 'replace'` and `pre_import_backup`. For export, call `exportServiceDatabase`, create an object URL, and download it. Show `database.missing` as “Missing on engine” with an Unlink action.

- [ ] **Step 4: Re-run**

Run: `cd ui; npx vitest run src/lib/components/service/DatabaseTab.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add ui/src/lib/components/service/DatabaseTab.svelte ui/src/lib/components/service/DatabaseFormDialog.svelte ui/src/lib/components/service/DatabaseTab.test.ts
git commit -m "feat(ui): add service databases tab"
```

---

### Task 3: Users tab, grants, and audited passwords

**Files:**
- Create: `ui/src/lib/components/service/UsersTab.svelte`
- Create: `ui/src/lib/components/service/UserFormDialog.svelte`
- Create: `ui/src/lib/components/service/GrantEditor.svelte`
- Test: `ui/src/lib/components/service/UsersTab.test.ts`

**Interfaces:**
- Consumes: `listServiceUsers`, `createServiceUser`, `updateServiceUser`, `deleteServiceUser`, `setServiceUserPassword`, `revealServiceUserPassword`.
- Produces: `UsersTab` with props `{ service: ServiceView; users: DatabaseUser[]; databases: Database[]; onRefresh: () => void }`.

- [ ] **Step 1: Write the failing tests**

```ts
it('shows managed and external users with grants', () => {
  render(UsersTab, { props: { service: MYSQL_SERVICE, users: MYSQL_USERS, databases: MYSQL_DATABASES, onRefresh: vi.fn() } });
  expect(screen.getByText('app')).toBeInTheDocument();
  expect(screen.getByText('external')).toBeInTheDocument();
  expect(screen.getByText('read/write + schema on app')).toBeInTheDocument();
});

it('reveals a password only after the audited call', async () => {
  vi.mocked(revealServiceUserPassword).mockResolvedValue({ value: 's3cret' });
  render(UsersTab, { props: { service: MYSQL_SERVICE, users: MYSQL_USERS, databases: MYSQL_DATABASES, onRefresh: vi.fn() } });
  expect(screen.queryByText('s3cret')).not.toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: /reveal password for app/i }));
  expect(await screen.findByText('s3cret')).toBeInTheDocument();
  expect(revealServiceUserPassword).toHaveBeenCalledWith('mysql', 'usr_app');
});

it('blocks deleting a user linked to a site', () => {
  render(UsersTab, { props: { service: MYSQL_SERVICE, users: [LINKED_USER], databases: MYSQL_DATABASES, onRefresh: vi.fn() } });
  expect(screen.getByRole('button', { name: /delete app/i })).toBeDisabled();
  expect(screen.getByText(/unlink sites/i)).toBeInTheDocument();
});
```

- [ ] **Step 2: Run and watch fail**

Run: `cd ui; npx vitest run src/lib/components/service/UsersTab.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement the tab**

Render username, host, managed/external badge, enabled state, password state, grant summary, and linked sites. Create/edit dialogs use `UserFormDialog` with `GrantEditor` (preset select plus whitelisted custom toggles). Password actions: Set/reset (calls `setServiceUserPassword`), Reveal (calls `revealServiceUserPassword`, stores the value only in local state, hides on refresh/route change), Copy (only after reveal). Delete uses `ConfirmDialog requireText={user.username}`; if `linked_sites.length > 0`, disable confirm and show an Unlink sites action. PostgreSQL delete shows a “Reassign owned objects to devstack admin” checkbox and passes `reassign_owned: true`. Redis hides host/grants and shows `access_level` read-only/full.

- [ ] **Step 4: Re-run**

Run: `cd ui; npx vitest run src/lib/components/service/UsersTab.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add ui/src/lib/components/service/UsersTab.svelte ui/src/lib/components/service/UserFormDialog.svelte ui/src/lib/components/service/GrantEditor.svelte ui/src/lib/components/service/UsersTab.test.ts
git commit -m "feat(ui): add service users and grants tab"
```

---

### Task 4: Backups tab and Redis password tab

**Files:**
- Create: `ui/src/lib/components/service/BackupsTab.svelte`
- Create: `ui/src/lib/components/service/RedisTab.svelte`
- Test: `ui/src/lib/components/service/BackupsTab.test.ts`
- Test: `ui/src/lib/components/service/RedisTab.test.ts`

**Interfaces:**
- Consumes: `listServiceBackups`, `createServiceBackup`, `deleteServiceBackup`, `restoreServiceBackup`, `downloadServiceBackup`, `getDatabaseJob`, `cancelDatabaseJob`; `setServiceUserPassword` for Redis default user.
- Produces: `BackupsTab` with props `{ service: ServiceView; backups: DatabaseBackup[]; onRefresh: () => void }`; `RedisTab` with props `{ service: ServiceView; users: DatabaseUser[]; onRefresh: () => void }`.

- [ ] **Step 1: Write the failing tests**

```ts
it('lists and downloads backups while the service is stopped', async () => {
  render(BackupsTab, { props: { service: STOPPED_MYSQL, backups: MYSQL_BACKUPS, onRefresh: vi.fn() } });
  expect(screen.getByText('app')).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: /download app backup/i }));
  expect(downloadServiceBackup).toHaveBeenCalledWith('mysql', 'bkp_1');
  expect(screen.queryByText(/activate the service to download/i)).not.toBeInTheDocument();
});

it('requires the target name for overwrite restore', async () => {
  render(BackupsTab, { props: { service: MYSQL_SERVICE, backups: MYSQL_BACKUPS, onRefresh: vi.fn() } });
  await user.click(screen.getByRole('button', { name: /restore app backup/i }));
  const confirm = screen.getByRole('button', { name: /restore backup/i });
  expect(confirm).toBeDisabled();
  await user.type(screen.getByLabelText(/type “app” to confirm/i), 'app');
  expect(confirm).toBeEnabled();
});

it('shows Redis users and password without database tabs', () => {
  render(RedisTab, { props: { service: REDIS_SERVICE, users: REDIS_USERS, onRefresh: vi.fn() } });
  expect(screen.getByText('cache')).toBeInTheDocument();
  expect(screen.getByText('read-only')).toBeInTheDocument();
  expect(screen.queryByText(/databases/i)).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run and watch fail**

Run: `cd ui; npx vitest run src/lib/components/service/BackupsTab.test.ts src/lib/components/service/RedisTab.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement both tabs**

Backups tab: table with database, timestamp, size, kind, status, SHA-256, actions Download/Restore/Delete. Download works for installed-but-stopped services; create backup, restore, and import show an “Activate the service” error when `!service.active`. Restore defaults to a new database name and has an overwrite mode with typed confirmation. Delete uses typed confirmation of the backup id or database name. Poll `getDatabaseJob` while a job is queued/running and show progress; offer Cancel.

Redis tab: list ACL users with enabled state, `read-only`/`full`, linked sites (always empty in v1), Set/reset password, Reveal, Copy, Delete (protected `default` cannot be deleted). A separate Default password card calls `setServiceUserPassword('redis', 'default')` and reveals through `revealServiceUserPassword`. No database list, backup, import, or export.

- [ ] **Step 4: Re-run**

Run: `cd ui; npx vitest run src/lib/components/service/BackupsTab.test.ts src/lib/components/service/RedisTab.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add ui/src/lib/components/service/BackupsTab.svelte ui/src/lib/components/service/RedisTab.svelte ui/src/lib/components/service/BackupsTab.test.ts ui/src/lib/components/service/RedisTab.test.ts
git commit -m "feat(ui): add service backups and Redis tabs"
```

---

### Task 5: Lane audit and fix cycle

- [ ] **Step 1: Run the lane gate**

```powershell
cd ui
npm run check
npx vitest run src/routes/ServiceDetail.test.ts src/lib/components/service/DatabaseTab.test.ts src/lib/components/service/UsersTab.test.ts src/lib/components/service/BackupsTab.test.ts src/lib/components/service/RedisTab.test.ts
npm test -- --run
npm run build
cd ..
git status --short
```

Expected: all pass; clean tree after the final commit.

- [ ] **Step 2: Fresh audit**

Dispatch a fresh reviewer with the `superpowers:requesting-code-review` skill. It reads the spec, this plan, the foundation contract, and `git diff $base..HEAD`; runs the lane gate; returns spec-compliance and code-quality verdicts with Critical/Important/Minor findings.

- [ ] **Step 3: Fix and re-audit**

Fix every Critical/Important finding with a failing test first. Commit. Re-dispatch a fresh reviewer. Repeat until no Critical/Important findings.

- [ ] **Step 4: Write the lane report**

Write `.superpowers/lane-report.md` with branch, base SHA, commits, test commands/results, audit verdicts, residual risks, and any Ruling. Do not merge.

- [ ] **Step 5: Commit the final lane state**

```powershell
git add ui/src/routes/ServiceDetail.svelte ui/src/routes/ServiceDetail.test.ts ui/src/App.svelte ui/src/lib/components/service
git commit -m "chore(ui): finalize service database management lane"
```

Stop and report the branch, HEAD SHA, lane report path, and audit verdicts to the controller.
