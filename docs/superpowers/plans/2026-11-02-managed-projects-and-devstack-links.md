# Managed Projects and C:\devstack Links

Date: 2026-11-02
Status: in progress

## Goal

Make the managed `projects` roots a hard boundary for every site path, every
folder-browser request, and every manual path entry, on Windows and WSL. Then
create four clearly named links plus a complete `README.txt` under `C:\devstack`.

This is enforcement, not migration: the live install already has its only site
(`static`) under `/data/devstack/projects`.

## Policy

Allowed roots:

| Mode | Root |
|---|---|
| Windows (`windows`) | configured `projects_root`, default `%USERPROFILE%\.devstack\projects` |
| WSL (`wsl`) | `/data/devstack/projects` (fixed managed distro root) |
| Linux local (`local`) | configured `projects_root`, default `~/.devstack/projects` |

Rules:

- A site path must be a **strict descendant** of its mode's root. The root itself
  is invalid.
- Containment is component-aware: `projects-old` never matches `projects`.
- Windows comparison is case-insensitive.
- Symlinks/reparse points are resolved; a resolved path that escapes the root is
  rejected.
- Enforce on create, source-mode copy destination, and start/reconcile.
- Existing external sites are not deleted. They are blocked from starting and
  reported by `devstack doctor`.
- Browse and mkdir requests are confined to the same roots. Browsing the root
  itself is allowed; selecting it as a site folder is not.
- Document roots stay relative to the site folder.

Links (canonical paths are the real targets; links are navigation only):

| Link | Type | Target |
|---|---|---|
| `C:\devstack\Windows-Install` | junction | `%USERPROFILE%\.devstack` |
| `C:\devstack\Windows-Projects` | junction | `%USERPROFILE%\.devstack\projects` |
| `C:\devstack\WSL-Install` | directory symlink | `\\wsl.localhost\devstack\data\devstack` |
| `C:\devstack\WSL-Projects` | directory symlink | `\\wsl.localhost\devstack\data\devstack\projects` |

`C:\devstack\README.txt` explains all four links, that they are live views and
not copies, that the two project links are separate data sets, that the links
must not be used as the Add Site project folder, and how to repair them.

## Shared interface (Task 0)

Add to `internal/pathmode`:

```go
// WSLProjectsRoot is the fixed projects root inside the managed WSL distro.
const WSLProjectsRoot = "/data/devstack/projects"

// ErrOutsideProjectsRoot marks a path that is not a strict descendant of the
// managed projects root.
var ErrOutsideProjectsRoot = errors.New("pathmode: project folder is outside the managed projects root")

// ManagedProjectsRoot resolves the canonical root for mode. projectsRoot is the
// configured Windows/local root; home is the fallback base. WSL ignores both
// and returns WSLProjectsRoot.
func ManagedProjectsRoot(mode model.SourceMode, projectsRoot, home string) (string, error)

// ValidateManagedProjectPath confirms hostPath (windows/local) or backendPath
// (wsl) is a strict descendant of root. Windows/local existing prefixes are
// resolved and rechecked.
func ValidateManagedProjectPath(mode model.SourceMode, hostPath, backendPath, root string) error
```

`ManagedProjectsRoot` returns `filepath.Clean(projectsRoot)` when non-empty, else
`filepath.Join(home, "projects")`. Empty both is an error.

`ValidateManagedProjectPath`:

- WSL: target = `backendPath`; use `ContainsPath(root, target, false)` plus
  `target != root`.
- Windows/local: target = `hostPath`; use `ContainsPath(root, target, windows)`
  plus `target != root`, then `ValidateContainedPath(root, target, windows)`.
- Return errors wrapping `ErrOutsideProjectsRoot` with root and target in the
  message.

## Parallel lanes

Task 0 is committed on `master` first. Then four worktrees branch from that
commit. Each lane owns its files; docs are split to avoid conflicts.

### Lane A - service and doctor enforcement

Files: `internal/sites/service.go`, `internal/sites/source_copy.go`,
`internal/sites/fakes_test.go`, `internal/sites/*_test.go`,
`internal/doctor/checks.go`, `internal/doctor/doctor.go`,
`internal/doctor/doctor_test.go`.

Work:

1. Failing tests for `Create`:
   - exact root rejected;
   - sibling `projects-old` rejected;
   - valid child accepted;
   - Windows case variant accepted;
   - symlink escape rejected when the OS allows creating the link.
2. Failing tests for `ChangeSourceMode` destination: root rejected, outside
   rejected, valid child accepted, copy is not invoked for rejected paths.
3. Failing test for `Start`: seed a site with an external path directly in the
   store, then `Start` returns an error wrapping
   `pathmode.ErrOutsideProjectsRoot` and the backend never prepares.
4. Wire validation into `newSite`, `ChangeSourceMode` (after translation, before
   `CopySource`), and `startLocked` (before database guard/backend work).
5. Align `newTestService`: create `dir/projects`, set
   `cfg.ProjectsRoot = dir/projects`, put `fakes.root` at `dir/projects/project`.
   Keep all existing fixtures valid.
6. Add doctor check `projects-root` (title "Managed project folders"):
   - lists sites from the store;
   - determines mode per site (empty mode: Windows drive path => windows, UNC =>
     wsl, otherwise local on Linux / windows on Windows);
   - fails with a list of offending site names and the allowed root;
   - remediation tells the user to move the folder under the root and
     re-register the site;
   - `OK` when every site is inside; skipped/OK when there are no sites.
7. Add doctor tests for inside, outside, mixed, and empty.

Commit when `go test ./internal/sites ./internal/doctor ./internal/pathmode`
passes.

### Lane B - API, settings, OpenAPI

Files: `internal/api/handlers_fs.go`, `internal/api/server.go`,
`internal/api/config` wiring in `internal/daemon/daemon.go` and
`internal/daemon/windows.go`, `internal/api/*_test.go`, `api/openapi.yaml`.

Work:

1. Failing tests for `GET /fs`:
   - Windows browse defaults to and is confined to `projects_root`;
   - browsing the root succeeds;
   - browsing the root's parent or a sibling is `403 FORBIDDEN`;
   - a symlink inside the root that escapes is `403`;
   - mkdir outside the root is `403`;
   - WSL paths outside `/data/devstack/projects` are `403` and never reach the
     agent;
   - WSL root and child are allowed.
2. Replace the Windows `userHome()` base with `s.projectsRoot()`:
   `cfg.ProjectsRoot` when set, else `cfg.UserHome/projects` (backwards
   compatible for tests). Use it for browse and mkdir.
3. Add daemon-side WSL containment against `pathmode.WSLProjectsRoot` for both
   browse and mkdir, allowing the root itself.
4. Add `ProjectsRoot` and `WSLProjectsRoot` to `settingsResponse` and populate
   them from the effective values. Keep `projects_root` stable when the store
   overrides `home` but config already provides a root.
5. Set `ProjectsRoot` in the Linux daemon API config (Windows already sets it).
   Add `WSLProjectsRoot: pathmode.WSLProjectsRoot` in both daemons.
6. Map `pathmode.ErrOutsideProjectsRoot` to `400 PROJECT_OUTSIDE_ROOT` in
   `classifyError`; add the constant.
7. Update `api/openapi.yaml` settings schema and error code documentation.
8. Extend `TestFSBrowserConfinedToUserHome` (rename to projects root) and the WSL
   handler tests; add settings assertions.

Commit when `go test ./internal/api ./internal/daemon` passes.

### Lane C - UI browser, manual validation, mock

Files: `ui/src/lib/pathRoots.ts` (new), `ui/src/lib/pathRoots.test.ts` (new),
`ui/src/lib/components/PathBrowser.svelte`,
`ui/src/lib/components/PathBrowser.test.ts` (new),
`ui/src/routes/AddSite.svelte`, `ui/src/routes/AddSite.test.ts`,
`ui/src/lib/types.ts`, `ui/src/lib/mock.ts`, `ui/src/lib/mock.test.ts`,
`ui/tests/e2e.spec.ts`.

Work:

1. Failing unit tests for pure helpers:
   - `normalizePathForCompare`, `isStrictlyInsideRoot`, `clampToRoot`,
     `crumbsBelowRoot`, `parentWithinRoot`;
   - exact root rejected for sites; `projects-old` rejected; Windows
     case-insensitive; trailing separators normalized; `..` rejected;
   - WSL accepts `/data/devstack/projects/app` and
     `\\wsl.localhost\devstack\data\devstack\projects\app`; rejects distro
     roots, `/data/devstack/projects-old`, and other distros.
2. `PathBrowser` props `rootPath` and `rootLabel`:
   - browse starts at the clamped initial path;
   - Up is disabled at the root;
   - root crumb shows the label (`Windows Projects` / `WSL Projects`), never the
     drive/house;
   - crumbs only include segments below the root;
   - "Go to root" becomes "Go to projects root" (or the label);
   - mode/root change while open clamps back into the new root;
   - parent fallback never leaves the root.
3. `AddSite`:
   - `browseRootFor` uses `settings.projects_root` /
     `settings.wsl_projects_root` with safe fallbacks;
   - `defaultPathFor` uses the same roots;
   - manual path validation rejects the root, outside paths, `..`, and wrong
     namespaces with a clear inline message:
     `Project folder must be inside <root>. Choose or create a folder under
     Windows Projects.` (or `WSL Projects`);
   - the root path itself gets: `Choose a subfolder inside <root>, not the
     projects root itself.`;
   - pass `rootPath`/`rootLabel` to `PathBrowser`;
   - reset/clamp the path when the mode changes and the path no longer fits.
4. `types.ts`: add `projects_root?: string` and `wsl_projects_root?: string`.
5. `mock.ts`: seed `projects_root: '/home/dev/.devstack/projects'` and
   `wsl_projects_root: '/data/devstack/projects'`; add the projects directory
   tree used by the browser; make `browseFs`/`mkdirFs` respect the mock root.
6. Update `AddSite.test.ts` fixtures to the managed roots; add rejection tests
   for outside/root paths in Windows, WSL, and local modes.
7. Update `ui/tests/e2e.spec.ts`: the browser now opens at the projects root and
   the root crumb is `WSL Projects`; click `scratch` directly.
8. Add `PathBrowser.test.ts`: Up disabled at root, label crumb, clamped initial
   path.

Commit when `npm test` (in `ui`) passes.

### Lane D - installer links, README, docs

Files: `build/windows/devstack-links.ps1` (new),
`build/windows/devstack-links.test.ps1` (new), `install.ps1`,
`docs/install-windows.md`, `docs/windows-paths.md`, `docs/sites.md`,
`docs/architecture.md`, `docs/known-issues.md`.

Work:

1. Add `build/windows/devstack-links.ps1` with:
   - `Set-DevstackDirectoryLink` (deletes only reparse points with
     `[System.IO.Directory]::Delete(path, $false)`, refuses real files/dirs,
     creates junctions or symlinks);
   - `Write-DevstackLinksReadme` (UTF-8 without BOM, complete text);
   - `Install-DevstackLinks` (validates `C:\devstack`, skips when the install
     root is `C:\devstack`, creates all four links, writes README, returns a
     result object with `Created`, `Skipped`, `Warnings`).
   - Parameters: `LinksRoot`, `WindowsRoot`, `WSLDistro`, `WSLInstallTarget`,
     `WSLProjectsTarget`, `SkipWSL`, `DryRun`.
2. Add `build/windows/devstack-links.test.ps1` as a plain PowerShell harness
   (no Pester): temp roots, two runs for idempotency, target checks, README
   content checks, refusal to overwrite a real directory, and dry-run writes
   nothing.
3. Dot-source the helper from `install.ps1` and call it:
   - upgrade success before `Upgrade completed successfully`;
   - `-SkipWSL` fresh completion;
   - full fresh completion after the WSL phase.
   Never create links when `$targetDir` is `C:\devstack`.
4. Replace the "Optional compatibility junction" section in
   `docs/install-windows.md` with the real `C:\devstack` folder and four links.
5. Document the hard restriction in `docs/windows-paths.md`, `docs/sites.md`,
   `docs/architecture.md`; update K7 in `docs/known-issues.md`.
6. Run the PowerShell harness and paste its output into the commit message or
   PR notes.

Commit when the harness passes.

## Merge and verification

1. Merge lanes A-D into `master` (no squash; keep lane commits).
2. Resolve conflicts by combining intent, never dropping tests.
3. Run:
   - `go test ./...`
   - `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...` and `go vet ./...`
   - `cd ui; npm test`
   - `cd ui; npm run check` (if present)
   - the PowerShell link harness
   - Playwright E2E in mock mode for the Add Site flow
4. Fix every failure; commit and push.

## Review protocol

- Round 1: one combined review-and-fix run. A fresh reviewer inspects the whole
  diff against this plan, fixes every finding in the same run, runs the tests,
  and commits. No separate review-only run.
- Round 2: one final combined review-and-fix run after Round 1 is merged and
  verified. Same rules: review and fix in one run, tests, commit.
- Both rounds must check: policy bypasses, root/child boundaries, case folding,
  symlink/reparse escape, WSL UNC aliases, browser escape, manual path escape,
  installer idempotency, README accuracy, and secret leakage.

## Deploy and real-install verification

1. Push `master`.
2. Run the transactional upgrade: `install.ps1 -Upgrade` (backup/rollback path).
3. Verify:
   - four links exist with correct types and targets (`fsutil reparsepoint
     query`, not PowerShell 5.1 `.Target`);
   - `C:\devstack\README.txt` exists and names all four links and targets;
   - `GET /api/v1/settings` returns non-empty `projects_root` and
     `wsl_projects_root`;
   - browsing above the root returns `403`;
   - creating a site outside the root returns `PROJECT_OUTSIDE_ROOT`;
   - the existing `static` site still starts and serves.
4. Do not print or persist any token or password.

## Progress ledger

- [x] Task 0: shared pathmode helper + tests, committed on master
- [x] Worktrees created and four lanes dispatched
- [x] Lane A merged
- [x] Lane B merged
- [x] Lane C merged
- [x] Lane D merged
- [x] Full verification green
  - `go test ./... -count=1`
  - `go vet ./...`
  - `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...`
  - UI `npm test` (370 tests) and `npm run check` (0 errors, 0 warnings)
  - Playwright Add Site flows (2 passed)
  - `build/windows/devstack-links.test.ps1` (87 assertions on PowerShell 5.1 and 7)
- [ ] Combined review-and-fix round 1
- [ ] Combined review-and-fix round 2
- [ ] Pushed, upgraded, real install verified
