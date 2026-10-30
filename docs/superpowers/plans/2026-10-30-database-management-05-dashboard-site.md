# Dashboard Site Database Flow Lane Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task in an isolated worktree. Steps use checkbox (`- [ ]`) syntax for tracking. Do not merge; the controller merges after audit.

**Goal:** Add an optional database panel to Add Site and a Databases & credentials card to Site Detail, using the frozen database API contract.

**Architecture:** Add Site keeps its three-step wizard (`Project → Runtime → Options`); the database panel lives in the existing Options step (step 3). The site creation payload gains an optional `database` object. Site Detail loads its links alongside the existing site data and renders a card between Environment and Terminal. All secrets are masked until an audited reveal.

**Tech Stack:** Svelte 5 runes, TypeScript, Vitest, Testing Library, existing `Card`/`Modal`/`ConfirmDialog`/`Button`/`Badge`/`ErrorBanner`/`Field`/`Toggle` components.

**Spec:** `docs/superpowers/specs/2026-10-30-database-management-design.md`

**Foundation:** `docs/superpowers/plans/2026-10-30-database-management-00-foundation.md`

**Base:** the frozen foundation SHA recorded in `.superpowers/db-foundation-sha.txt`.

## Global Constraints

- The database panel is optional and never blocks site creation when no active SQL service exists.
- The Options step is step 3 in the current wizard; do not renumber the wizard.
- Nothing is provisioned before final submit. The review step never displays a password.
- Selecting an existing user never changes grants during site creation. Missing access shows a warning and a Manage database user link.
- External users may be linked with an optional password; blank means metadata-only and omits `DB_PASSWORD`/`DATABASE_URL`.
- Site deletion removes only links; the confirmation says databases, users, grants, passwords, backups, and data remain.
- Passwords are masked until an explicit audited reveal and are cleared on route change/refresh.
- MySQL/MariaDB and PostgreSQL are the only selectable services in Add Site; Redis is not site-linkable.
- All existing Add Site and Site Detail tests must stay green.

## Review Focus

- The review step must not contain the generated or entered password anywhere in the DOM.
- An existing user without access must show a warning and a Manage database user action, and the create payload must not add a grant.
- A metadata-only external-user link must omit password env values and show “Password not managed.”
- The site delete confirmation must explicitly say linked databases and data remain.
- A linked service that is stopped or shadowed by the other MySQL-family engine must show an offline/blocking state, not a silent success.

## Files Owned

- Modify: `ui/src/routes/AddSite.svelte`
- Modify: `ui/src/routes/AddSite.test.ts`
- Modify: `ui/src/routes/SiteDetail.svelte`
- Modify: `ui/src/routes/SiteDetail.test.ts`
- Create: `ui/src/lib/components/site/DatabasePicker.svelte`
- Create: `ui/src/lib/components/site/DatabaseReviewRow.svelte`
- Create: `ui/src/lib/components/site/SiteDatabaseCard.svelte`
- Create: `ui/src/lib/components/site/LinkDatabaseDialog.svelte`
- Test: colocated `*.test.ts`

Do not edit `ui/src/lib/databaseTypes.ts`, `databaseApi.ts`, `databaseMock.ts`, `SecretField.svelte`, `DatabaseConnectionCard.svelte`, `types.ts`, `api.ts`, `mock.ts`, `ServiceDetail.svelte`, or any Go file. Request a foundation amendment if a contract is missing.

## Interfaces

Consumes from foundation:

```ts
// ui/src/lib/databaseTypes.ts
CreateSiteDatabaseRequest, Database, DatabaseUser, SiteDatabaseLink,
SiteDatabaseResult, Secret

// ui/src/lib/databaseApi.ts
listServiceDatabases(id): Promise<{ databases: Database[] }>
listServiceUsers(id): Promise<{ users: DatabaseUser[] }>
listSiteDatabases(ref): Promise<{ links: SiteDatabaseLink[] }>
linkSiteDatabase(ref, body): Promise<SiteDatabaseLink>
unlinkSiteDatabase(ref, linkID): Promise<void>
createSiteDatabase(ref, body): Promise<SiteDatabaseResult>
revealServiceUserPassword(id, userID): Promise<Secret>
setServiceUserPassword(id, userID): Promise<Secret>
```

`CreateSiteRequest` in `ui/src/lib/types.ts` is extended by foundation with:

```ts
database?: CreateSiteDatabaseRequest
```

Existing API: `listServices()`, `createSite(body)`, `getSite(ref)`, `restartSite(ref)`.

Shared components: `SecretField.svelte`, `DatabaseConnectionCard.svelte`.

Existing: `Card`, `Modal`, `ConfirmDialog`, `Button`, `Badge`, `ErrorBanner`, `Field`, `Toggle`.

Produces for the controller: Add Site database panel, post-create reveal, Site Detail card, and link dialog. No other lane imports this lane's files.

---

### Task 1: Add Site database panel in the Options step

**Files:**
- Modify: `ui/src/routes/AddSite.svelte` (step 3 block, currently lines 1439-1554; state near lines 54-90; validation near lines 316-470)
- Create: `ui/src/lib/components/site/DatabasePicker.svelte`
- Test: `ui/src/routes/AddSite.test.ts`

**Interfaces:**
- Consumes: `listServices`, `listServiceDatabases`, `listServiceUsers`.
- Produces: `DatabasePicker` with bindable `value: CreateSiteDatabaseRequest | null`; emits `manageUser(serviceID, userID)`.

- [ ] **Step 1: Write the failing panel test**

```ts
it('adds an optional database panel in the Options step', async () => {
  await fillProject();
  await user.click(screen.getByRole('button', { name: 'Continue' }));
  await user.click(screen.getByRole('button', { name: 'Continue' }));
  expect(await screen.findByText('Database')).toBeInTheDocument();
  expect(screen.getByLabelText('Create or link a database')).not.toBeChecked();
  expect(screen.getByText(/no database will be linked/i)).toBeInTheDocument();
});

it('builds a new database and new user payload', async () => {
  vi.mocked(api.listServices).mockResolvedValue({ services: [MYSQL_SERVICE] });
  vi.mocked(db.listServiceDatabases).mockResolvedValue({ databases: [] });
  vi.mocked(db.listServiceUsers).mockResolvedValue({ users: [] });
  await fillProject();
  await user.click(screen.getByRole('button', { name: 'Continue' }));
  await user.click(screen.getByRole('button', { name: 'Continue' }));
  await user.click(screen.getByLabelText('Create or link a database'));
  await user.selectOptions(screen.getByLabelText('Database service'), 'mysql');
  await user.click(screen.getByLabelText('Create new database'));
  await user.type(screen.getByLabelText('Database name'), 'blog');
  await user.click(screen.getByLabelText('Create new user'));
  await user.type(screen.getByLabelText('Database username'), 'blog_user');
  expect(screen.getByText('Read/write + schema')).toBeInTheDocument();
});
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd ui; npx vitest run src/routes/AddSite.test.ts -t "database"`
Expected: FAIL, no Database panel.

- [ ] **Step 3: Implement the panel**

Add state near the existing wizard state: `dbEnabled = $state(false)`, `dbService = $state('')`, `dbMode = $state<'new'|'existing'>('new')`, `dbName`, `dbUserMode`, `dbUserID`, `dbUsername`, `dbPassword`, `dbGrant`. Load active SQL services with `listServices()` when the panel is enabled; filter `installed && active && (id === 'mysql' || id === 'mariadb' || id === 'postgres')`. Load databases/users when the service changes. Default new database name to `slugify(name) || 'app'`; default new username to the same slug; default grant `read_write_schema`. For an existing user, show a grants table for the selected database; if no grant, show a warning and a `Manage database user` link to `#/services/{serviceID}?tab=users&user={userID}`. Do not call any grant API. Include an optional password field for external users with helper text “Leave blank to link without a password.” Render the panel in the step-3 Options block after the TLS/www/SPA toggles and before the review rows.

- [ ] **Step 4: Re-run the test**

Run: `cd ui; npx vitest run src/routes/AddSite.test.ts -t "database"`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add ui/src/routes/AddSite.svelte ui/src/routes/AddSite.test.ts ui/src/lib/components/site/DatabasePicker.svelte
git commit -m "feat(ui): add optional database panel to Add Site"
```

---

### Task 2: Validation, review, create payload, and post-create reveal

**Files:**
- Modify: `ui/src/routes/AddSite.svelte` (`validateAll`, `fieldStep`, `stepFields`, `focusField`, review block, `create()`)
- Create: `ui/src/lib/components/site/DatabaseReviewRow.svelte`
- Test: `ui/src/routes/AddSite.test.ts`

**Interfaces:**
- Consumes: `createSite`, `revealServiceUserPassword`.
- Produces: `create()` sends `database` only when enabled; success state with a one-time reveal before navigation.

- [ ] **Step 1: Write the failing payload and review tests**

```ts
it('sends the exact database payload and omits it when disabled', async () => {
  await fillProject();
  await user.click(screen.getByRole('button', { name: 'Continue' }));
  await user.click(screen.getByRole('button', { name: 'Continue' }));
  await user.click(screen.getByRole('button', { name: 'Create site' }));
  expect(api.createSite).toHaveBeenCalledWith(expect.not.objectContaining({ database: expect.anything() }));
});

it('shows connection values but never a password in review', async () => {
  // enable the panel and choose new database + new user with a password
  await user.click(screen.getByRole('button', { name: 'Create site' }));
  expect(screen.queryByText('s3cret')).not.toBeInTheDocument();
  expect(screen.getByText('Password will be generated when the site is created.')).toBeInTheDocument();
});

it('reveals the new password only after creation', async () => {
  vi.mocked(api.createSite).mockResolvedValue(SITE);
  vi.mocked(db.listSiteDatabases).mockResolvedValue({ links: [SITE_DB_LINK] });
  vi.mocked(db.revealServiceUserPassword).mockResolvedValue({ value: 'generated' });
  await user.click(screen.getByRole('button', { name: 'Create site' }));
  expect(await screen.findByText('Database created')).toBeInTheDocument();
  expect(screen.queryByText('generated')).not.toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Reveal password' }));
  expect(await screen.findByText('generated')).toBeInTheDocument();
});
```

- [ ] **Step 2: Run and watch fail**

Run: `cd ui; npx vitest run src/routes/AddSite.test.ts -t "database"`
Expected: FAIL, payload/review missing.

- [ ] **Step 3: Implement validation, review, and submit**

Add database error keys to `validateAll`: service required, database name required for new, existing database required, user required for existing, username required for new, external password optional. Map them to step 3 in `fieldStep`/`stepFields` and focus them in `focusField`. Add a `DatabaseReviewRow` to the review rows showing service, database, username, grant, host, and port; it must not render a password. In `create()`, build `database` only when `dbEnabled`:

```ts
database: dbEnabled ? {
  service_id: dbService,
  ...(dbMode === 'new' ? { new_database: { name: dbName } } : { database: dbName }),
  ...(dbUserMode === 'new'
    ? { new_user: { username: dbUsername, grants: [{ database: dbName, preset: dbGrant }] } }
    : { user_id: dbUserID, ...(dbPassword ? { password: dbPassword } : {}) }),
  grant: dbGrant,
  primary: true,
} : undefined
```

Keep the existing error scoping. After `createSite` returns, if `database` was enabled, call `listSiteDatabases(site.id)` to load the new link, then open a `Database created` modal with `DatabaseConnectionCard` and a `SecretField` that calls `revealServiceUserPassword(serviceID, userID)` only on Reveal; the Continue button pushes `/sites/{id}`. If the user closes the modal, push without revealing. On create failure, stay on step 3 and show the error; if the error details include a database provisioning warning, display it above the form.

- [ ] **Step 4: Re-run**

Run: `cd ui; npx vitest run src/routes/AddSite.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add ui/src/routes/AddSite.svelte ui/src/routes/AddSite.test.ts ui/src/lib/components/site/DatabaseReviewRow.svelte
git commit -m "feat(ui): review and reveal Add Site database credentials"
```

---

### Task 3: Site Detail Databases & credentials card

**Files:**
- Modify: `ui/src/routes/SiteDetail.svelte` (load block lines 285-360; aside lines 976-1166; insert before `TerminalPanel` line 1169; delete dialog lines 1950-1964)
- Create: `ui/src/lib/components/site/SiteDatabaseCard.svelte`
- Create: `ui/src/lib/components/site/LinkDatabaseDialog.svelte`
- Test: `ui/src/routes/SiteDetail.test.ts`

**Interfaces:**
- Consumes: `listSiteDatabases`, `linkSiteDatabase`, `unlinkSiteDatabase`, `createSiteDatabase`, `revealServiceUserPassword`, `setServiceUserPassword`, `restartSite`.
- Produces: `SiteDatabaseCard` with props `{ site: Site; links: SiteDatabaseLink[]; onRefresh: () => void; onRestart: () => void }`.

- [ ] **Step 1: Write the failing card tests**

```ts
it('shows linked database connection details with a masked password', async () => {
  renderSite('blog');
  expect(await screen.findByText('Databases')).toBeInTheDocument();
  expect(screen.getByText('app')).toBeInTheDocument();
  expect(screen.getByText('127.0.0.1:3306')).toBeInTheDocument();
  expect(screen.getByText('blog_user')).toBeInTheDocument();
  expect(screen.queryByText('s3cret')).not.toBeInTheDocument();
});

it('reveals a linked password through the audited API', async () => {
  vi.mocked(db.revealServiceUserPassword).mockResolvedValue({ value: 's3cret' });
  renderSite('blog');
  await user.click(await screen.findByRole('button', { name: /reveal password for app/i }));
  expect(await screen.findByText('s3cret')).toBeInTheDocument();
  expect(db.revealServiceUserPassword).toHaveBeenCalledWith('mysql', 'usr_app');
});

it('shows password not managed for a metadata-only link', async () => {
  vi.mocked(db.listSiteDatabases).mockResolvedValue({ links: [METADATA_ONLY_LINK] });
  renderSite('blog');
  expect(await screen.findByText('Password not managed')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: /set password/i })).toBeInTheDocument();
});

it('says linked databases remain when deleting the site', async () => {
  renderSite('blog');
  await user.click(await screen.findByRole('button', { name: /delete site/i }));
  expect(screen.getByText(/databases, users, grants, passwords, backups, and data remain/i)).toBeInTheDocument();
});
```

- [ ] **Step 2: Run and watch fail**

Run: `cd ui; npx vitest run src/routes/SiteDetail.test.ts -t "database"`
Expected: FAIL, no Databases card.

- [ ] **Step 3: Implement the card**

Load links in the existing `Promise.all` with a separate catch (`getSite` failure still wins). Insert `Card title="Databases"` before `TerminalPanel`. Each row shows engine, host:port, database, username, primary badge, password state, and actions: Copy host/port/database/username, Reveal/Copy password when managed, Set password when metadata-only, Copy `.env` snippet, Unlink. Use `DatabaseConnectionCard` for the connection block. Shared users show a “shared user” badge and a Manage link to `#/services/{serviceID}?tab=users&user={userID}`. `Link database` opens `LinkDatabaseDialog` with service/database/user selection and the same create-or-link choices; on success call `onRefresh`. Unlink uses `ConfirmDialog`. After Set password or a grant-changing action, show “Restart site to apply” with a button calling `restartSite`. If `link.available === false`, show “Database offline” with an Activate link; if `link.error` mentions the exclusive engine conflict, show the blocking message.

Update the existing delete `ConfirmDialog` description to include: “Linked databases, users, grants, passwords, backups, and data remain.” Do not change the delete API call.

- [ ] **Step 4: Re-run**

Run: `cd ui; npx vitest run src/routes/SiteDetail.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add ui/src/routes/SiteDetail.svelte ui/src/routes/SiteDetail.test.ts ui/src/lib/components/site/SiteDatabaseCard.svelte ui/src/lib/components/site/LinkDatabaseDialog.svelte
git commit -m "feat(ui): show site database credentials and links"
```

---

### Task 4: Lane audit and fix cycle

- [ ] **Step 1: Run the lane gate**

```powershell
cd ui
npm run check
npx vitest run src/routes/AddSite.test.ts src/routes/SiteDetail.test.ts
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
git add ui/src/routes/AddSite.svelte ui/src/routes/AddSite.test.ts ui/src/routes/SiteDetail.svelte ui/src/routes/SiteDetail.test.ts ui/src/lib/components/site
git commit -m "chore(ui): finalize site database flow lane"
```

Stop and report the branch, HEAD SHA, lane report path, and audit verdicts to the controller.
