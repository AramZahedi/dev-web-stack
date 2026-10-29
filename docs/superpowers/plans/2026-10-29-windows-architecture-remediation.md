# Windows Architecture Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transition the Windows platform default root completely to the user profile (`%USERPROFILE%\.devstack`), eliminate all hardcoded `C:\devstack` references, implement segregated WSL vs. Windows directory browsing with automatic project path slug prefilling in Add Site, and fix Composer tool installation on Windows.

**Architecture:** 
1. Canonicalize Windows root to `%USERPROFILE%\.devstack` across platform resolvers, configuration loaders, CLI/client helpers, installer scripts, and Windows helper configs.
2. Extend WSL agent with a protected `/v1/fs` directory browsing endpoint; update daemon `/api/v1/fs` to route to WSL agent when `mode=wsl` and to host filesystem when `mode=windows`.
3. Enhance UI `PathBrowser.svelte` to support both Windows and Linux path paradigms and update `AddSite.svelte` with reactive slug-based path prefilling.
4. Wire Composer tool installation on Windows to provision Composer inside the WSL Debian distro and generate Windows `.cmd` command shims while properly reporting `installed: true` status.

**Tech Stack:** Go 1.24, Svelte 5 (Runes), TypeScript, Tailwind CSS, PowerShell, WSL2 (Debian 12).

**Spec:** `docs/superpowers/specs/2026-10-26-windows-wsl-design.md`, `docs/windows-paths.md`, `docs/architecture.md`.

## Global Constraints

- Never use `C:\devstack` on Windows; all configuration, runtimes, state, binaries, and projects default under `%USERPROFILE%\.devstack`.
- Directory browsing in WSL mode must strictly query Linux ext4 paths (`/data/devstack/projects`) and never disclose Windows drive paths.
- Directory browsing in Windows mode must strictly query Windows NTFS paths (`%USERPROFILE%\.devstack\projects` or user home) and never disclose WSL ext4 paths.
- All background processes and commands must execute completely headless (no visible terminal windows).
- Preserve existing comments and docstrings across modified files.

## Review Focus

1. `PathBrowser.svelte` breadcrumb and path parsing when navigating root drives on Windows (`C:\`) vs. Linux (`/`).
2. Security boundary enforcement in `/api/v1/fs` ensuring users cannot traverse outside allowed roots on either host or WSL.
3. Slugification in `AddSite.svelte` ensuring non-alphanumeric characters, spaces, and edge cases are safely transformed without erasing user-entered custom paths.
4. Composer command shim translation in `%USERPROFILE%\.devstack\bin\composer.cmd` executing properly through `wsl.exe` without path truncation.
5. Windows helper service installation when configured with user profile path arguments.

---

### Task 1: Spec and Architecture Documentation Overhaul

**Files:**
- Modify: `docs/superpowers/specs/2026-10-26-windows-wsl-design.md`
- Modify: `docs/windows-paths.md`
- Modify: `docs/architecture.md`
- Modify: `docs/install-windows.md`
- Modify: `docs/decisions.md`

**Interfaces:**
- Consumes: User directives on Windows user root, segregated browsing, slug prefilling, and tool installation.
- Produces: Updated specifications and decision records governing Windows architecture.

- [ ] **Step 1: Update design specification in `docs/superpowers/specs/2026-10-26-windows-wsl-design.md`**
  Update Section 4 to define `%USERPROFILE%\.devstack` (or `$USER_ROOT/.devstack`) as the canonical Windows application root and `%USERPROFILE%\.devstack\projects` as the default project root. Add specifications for `/api/v1/fs` mode segregation and Composer provisioning in WSL.

- [ ] **Step 2: Update `docs/windows-paths.md` and `docs/architecture.md`**
  Replace references to `C:\devstack` with `%USERPROFILE%\.devstack`. Document the segregated filesystem browsing behavior and project directory prefilling.

- [ ] **Step 3: Update `docs/install-windows.md` and `docs/decisions.md`**
  Record Decision S82 (or update S56) codifying that Windows uses the user root directory `%USERPROFILE%\.devstack` and skips `C:\` completely.

- [ ] **Step 4: Commit documentation updates**
  ```bash
  git add docs/
  git commit -m "docs: align specs with user root directory on Windows and segregated browsing"
  ```

---

### Task 2: Windows Root Canonicalization Across Go Codebase and Installers

**Files:**
- Modify: `internal/platform/platform.go:10-35`
- Modify: `internal/platform/platform_test.go:1-35`
- Modify: `internal/config/config.go:240-275`
- Modify: `internal/cli/root.go:360-380`
- Modify: `internal/client/client.go:115-135`
- Modify: `internal/daemon/windows.go:910-960`
- Modify: `internal/helper/windows_config.go:10-26`
- Modify: `cmd/devstack-installer/main.go:160-175`
- Modify: `install.ps1:10-30`
- Modify: `uninstall.ps1:15-40`
- Modify: `build/windows/devstack-daemon-task.ps1:1-15`

**Interfaces:**
- Consumes: `os.UserHomeDir()`
- Produces: `platform.DefaultRoot(os)` returning `%USERPROFILE%\.devstack` on Windows.

- [ ] **Step 1: Write failing test in `internal/platform/platform_test.go`**
  Verify `DefaultRoot("windows")` returns `filepath.Join(userHome, ".devstack")` and `DefaultProjectsRoot` points to `projects` under user root.

- [ ] **Step 2: Run test to verify failure**
  Run: `go test -v ./internal/platform`
  Expected: FAIL (currently expects `C:\devstack`)

- [ ] **Step 3: Implement user root resolution in `internal/platform/platform.go`**
  Update `DefaultRoot` for Windows to resolve via `os.UserHomeDir()` -> `filepath.Join(home, ".devstack")`. Fall back to `%USERPROFILE%\.devstack` if lookup fails.

- [ ] **Step 4: Update `internal/config/config.go`, `internal/cli/root.go`, and `internal/client/client.go`**
  Ensure consistent resolution across CLI, Client, and Daemon to use `platform.DefaultRoot("windows")` when `DEVSTACK_HOME` and `--home` are unset.

- [ ] **Step 5: Update `internal/daemon/windows.go` and `internal/helper/windows_config.go`**
  Replace hardcoded `C:\devstack` fallback in `helperAdapter.ToolStatus`, `ToolInstall`, and helper configuration defaults with dynamic user home resolution.

- [ ] **Step 6: Update `install.ps1`, `uninstall.ps1`, and scheduled task scripts**
  Change `$InstallDir` default from `C:\devstack` to `"$env:USERPROFILE\.devstack"`.

- [ ] **Step 7: Run platform and config tests**
  Run: `go test -v ./internal/platform ./internal/config`
  Expected: PASS

- [ ] **Step 8: Commit changes**
  ```bash
  git add internal/platform internal/config internal/cli internal/client internal/daemon internal/helper install.ps1 uninstall.ps1 build/windows/
  git commit -m "fix(platform): default Windows root to user profile .devstack instead of C drive"
  ```

---

### Task 3: WSL Agent Filesystem Browsing API and Daemon Routing

**Files:**
- Modify: `internal/agentproto/protocol.go`
- Create: `internal/agent/fs.go`
- Create: `internal/agent/fs_test.go`
- Modify: `internal/agent/server.go`
- Modify: `internal/agentclient/client.go`
- Modify: `internal/api/handlers_fs.go`
- Modify: `internal/api/handlers_fs_test.go`

**Interfaces:**
- Consumes: Agent HTTP server, agentclient, daemon API server.
- Produces: `GET /v1/fs?path=...` in agent; `GET /api/v1/fs?path=...&mode=wsl|windows` in API server.

- [ ] **Step 1: Write failing test in `internal/agent/fs_test.go`**
  Test agent `/v1/fs` endpoint returns directories within `/data/devstack/projects` and rejects illegal path traversals.

- [ ] **Step 2: Run test to verify failure**
  Run: `go test -v ./internal/agent -run TestAgentFS`
  Expected: FAIL

- [ ] **Step 3: Implement `/v1/fs` in `internal/agent/fs.go` and register in `internal/agent/server.go`**
  Implement safe directory listing returning `agentproto.FSListing` containing directory entries only. Default to `/data/devstack/projects` if path is empty or `/`.

- [ ] **Step 4: Add `BrowseFS` method to `internal/agentclient/client.go`**
  Implement `c.do(ctx, http.MethodGet, agentproto.PathFS+"?path="+url.QueryEscape(path), nil, &resp)`.

- [ ] **Step 5: Write failing test in `internal/api/handlers_fs_test.go`**
  Test `GET /api/v1/fs?mode=wsl` routes to WSL agent and returns WSL directory entries, while `GET /api/v1/fs?mode=windows` browses Windows host directories.

- [ ] **Step 6: Update `internal/api/handlers_fs.go`**
  Inspect `mode` query parameter. If `mode == "wsl"` (or on Windows when path starts with `/` or `/data/`), delegate to `s.agentClient.BrowseFS(path)`. Otherwise, evaluate against host user home.

- [ ] **Step 7: Run agent and API tests**
  Run: `go test -v ./internal/agent ./internal/agentclient ./internal/api -run "FS"`
  Expected: PASS

- [ ] **Step 8: Commit changes**
  ```bash
  git add internal/agentproto internal/agent internal/agentclient internal/api
  git commit -m "feat(api): implement segregated WSL and Windows filesystem browsing"
  ```

---

### Task 4: Frontend PathBrowser and AddSite Segregated Browsing & Slug Auto-Prefill

**Files:**
- Modify: `ui/src/lib/api.ts`
- Modify: `ui/src/lib/components/PathBrowser.svelte`
- Modify: `ui/src/routes/AddSite.svelte`
- Modify: `ui/src/test/add-site.test.ts`
- Modify: `ui/src/test/path-browser.test.ts`

**Interfaces:**
- Consumes: `/api/v1/fs?path=...&mode=...`
- Produces: Segregated path browsing modal and automatic slug prefilling in Add Site.

- [ ] **Step 1: Write failing test in `ui/src/test/path-browser.test.ts`**
  Verify `PathBrowser` sends `mode` parameter to `browseFs` and formats breadcrumbs correctly for both Windows drive paths (`C:\Users\Aram\...`) and Linux paths (`/data/devstack/...`).

- [ ] **Step 2: Update `ui/src/lib/api.ts`**
  Update `browseFs(path: string, mode?: 'wsl' | 'windows'): Promise<FsListing>` to pass `mode` in query string.

- [ ] **Step 3: Update `ui/src/lib/components/PathBrowser.svelte`**
  Accept `mode?: 'wsl' | 'windows'` prop. Adapt `crumbs` and parent navigation to handle backslashes and Windows drive letters when `mode === 'windows'`, and forward slashes when `mode === 'wsl'`.

- [ ] **Step 4: Write failing test in `ui/src/test/add-site.test.ts`**
  Test that entering site name automatically slugifies and prefills `path`:
  - In WSL mode: `/data/devstack/projects/<slug>`
  - In Windows mode: `<home>/projects/<slug>`
  Test that toggling source mode updates path prefix while retaining slug, unless user manually edited the path.

- [ ] **Step 5: Implement slug auto-prefill and mode binding in `ui/src/routes/AddSite.svelte`**
  Implement `slugify(name: string): string`. Track `pathTouched: boolean`. Update `path` reactively when `name` changes if not touched. Pass `sourceMode` to `PathBrowser`. Default `initialBrowsePath` based on active mode (`/data/devstack/projects` for WSL, `<home>/projects` for Windows).

- [ ] **Step 6: Run frontend tests**
  Run: `npm --prefix ui test`
  Expected: PASS

- [ ] **Step 7: Commit frontend changes**
  ```bash
  git add ui/src/
  git commit -m "feat(ui): add segregated path browser and automatic site directory slug prefill"
  ```

---

### Task 5: Composer Tool Installation & Status on Windows

**Files:**
- Modify: `internal/daemon/windows.go:910-960`
- Modify: `internal/tooling/service.go:250-275`
- Modify: `internal/tooling/windows.go:20-60`
- Modify: `internal/tooling/windows_test.go`
- Modify: `cmd/devstack/main.go`

**Interfaces:**
- Consumes: WSL Debian distro execution (`wsl.exe -d devstack`), Windows bin directory.
- Produces: Working Composer inside WSL + `%USERPROFILE%\.devstack\bin\composer.cmd` with accurate `installed: true` status.

- [ ] **Step 1: Write failing test in `internal/tooling/windows_test.go`**
  Test that `helperAdapter.ToolStatus` and `helperAdapter.ToolInstall` correctly report `installed: true` and `managed: true` when shims and PHAR exist.

- [ ] **Step 2: Run test to verify failure**
  Run: `go test -v ./internal/tooling -run TestWindowsToolStatus`
  Expected: FAIL

- [ ] **Step 3: Implement Composer provisioning in WSL and fix Windows helper adapter**
  In `internal/tooling/service.go`, when running on Windows during `InstallTool("composer")`:
  - Run WSL command or copy Composer PHAR into WSL `/data/devstack/tools/composer/composer.phar` and create `/usr/local/bin/composer` (or `/data/devstack/bin/composer`).
  - In `internal/daemon/windows.go`: fix `helperAdapter.ToolStatus` and `helperAdapter.ToolInstall` to return `Installed: true` and `Managed: true` when `%USERPROFILE%\.devstack\bin\composer.cmd` exists.

- [ ] **Step 4: Run tooling tests**
  Run: `go test -v ./internal/tooling ./internal/daemon -run "Tool"`
  Expected: PASS

- [ ] **Step 5: Commit tooling changes**
  ```bash
  git add internal/tooling internal/daemon
  git commit -m "fix(tooling): support composer installation and accurate status on Windows"
  ```

---

### Task 6: Full Verification and Live System Validation

**Files:**
- Modify: `test/windows/lifecycle_test.ps1`
- Test: All unit and integration test suites

- [ ] **Step 1: Run complete Go test suite**
  Run: `go test ./...`
  Expected: PASS

- [ ] **Step 2: Run complete UI test suite**
  Run: `npm --prefix ui test`
  Expected: PASS

- [ ] **Step 3: Build frontend and Go binaries**
  Run: `npm --prefix ui run build` and `go build -o devstack.exe ./cmd/devstack`

- [ ] **Step 4: Validate live endpoints on running daemon**
  - Verify `GET /api/v1/tools` reports correct paths under `%USERPROFILE%\.devstack\bin`.
  - Verify `POST /api/v1/tools/composer/install` returns `installed: true`.
  - Verify `GET /api/v1/fs?mode=wsl` lists WSL directories.
  - Verify `GET /api/v1/fs?mode=windows` lists Windows directories.

- [ ] **Step 5: Commit and update tag/releases if needed**
  ```bash
  git add .
  git commit -m "chore: complete Windows architecture remediation and verification"
  ```
