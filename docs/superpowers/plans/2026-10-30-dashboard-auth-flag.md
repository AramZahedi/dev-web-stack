# Dashboard Auth Flag Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the dashboard install-token gate opt-in through `config.yaml` (`dashboard_auth`, default `false`) and `devstack init --dashboard-auth`.

**Architecture:** The config setting is the single source of truth. The daemon passes an empty `AuthToken` when the flag is off, `devstack ui` omits the `#token=` fragment when off, and `init --dashboard-auth` persists the setting and restarts a running daemon so it applies immediately.

**Tech Stack:** Go, Cobra, YAML config, Svelte UI (no UI changes), Windows live daemon.

**Spec:** `docs/superpowers/specs/2026-10-30-dashboard-auth-flag-design.md`

## Global Constraints

- Default `dashboard_auth` is `false`.
- Keep the `api-token` secret and all auth middleware for enabled installs.
- No installer prompt and no Settings API/UI toggle.
- Host allowlisting, loopback-only API access, and CSRF stay unchanged.
- All existing tests must stay green.

## Review Focus

- A fresh install with no config value must open the dashboard without a token fragment.
- An existing install that sets `dashboard_auth: true` must still return 403 without a token.
- `init --dashboard-auth=false` must turn an enabled gate off, not just write the file.
- `devstack ui` must not leak a stale token fragment when auth is off.
- Restarting the daemon during `init` must not strand the dashboard if a later init step fails.

---

### Task 1: Add the config setting

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `Config.DashboardAuth bool` with YAML key `dashboard_auth`.

- [ ] **Step 1: Write the failing test** — append to `config_test.go`:

```go
func TestDashboardAuthDefaultsOffAndRoundTrips(t *testing.T) {
	home := t.TempDir()
	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DashboardAuth {
		t.Fatal("DashboardAuth = true, want false")
	}
	cfg.DashboardAuth = true
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.DashboardAuth {
		t.Fatal("DashboardAuth = false after round-trip")
	}
}
```

- [ ] **Step 2: Run it and watch it fail** — `go test ./internal/config -run TestDashboardAuthDefaultsOffAndRoundTrips`. Expected: compile error, `cfg.DashboardAuth` undefined.
- [ ] **Step 3: Implement** — add `DashboardAuth bool \`yaml:"dashboard_auth"\`` after `AutoStartSites`, and `DashboardAuth: false` in `Default()`.
- [ ] **Step 4: Re-run** — same command. Expected: PASS.
- [ ] **Step 5: Commit** — `git add internal/config && git commit -m "feat(config): add dashboard_auth setting default off"`.

---

### Task 2: Gate daemon token loading

**Files:**
- Modify: `internal/daemon/auth.go`, `internal/daemon/daemon.go`, `internal/daemon/windows.go`
- Test: `internal/daemon/auth_test.go` (new)

**Interfaces:**
- Produces: `dashboardAuthToken(home string, enabled bool) (string, error)`; returns `"", nil` when disabled without creating the secret.

- [ ] **Step 1: Write the failing test** — create `auth_test.go`:

```go
func TestDashboardAuthTokenDisabledDoesNotCreateSecret(t *testing.T) {
	home := t.TempDir()
	token, err := dashboardAuthToken(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		t.Fatalf("token = %q, want empty", token)
	}
	if _, err := os.Stat(filepath.Join(home, "system", "secrets", "api-token")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("api-token created while disabled: %v", err)
	}
}

func TestDashboardAuthTokenEnabledCreatesSecret(t *testing.T) {
	home := t.TempDir()
	token, err := dashboardAuthToken(home, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 {
		t.Fatalf("token length = %d, want 64 hex chars", len(token))
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Fatalf("token is not hex: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "system", "secrets", "api-token")); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run it and watch it fail** — `go test ./internal/daemon -run TestDashboardAuthToken`. Expected: compile error, too many arguments.
- [ ] **Step 3: Implement** — change the signature, return early when `!enabled`, and update both call sites to `dashboardAuthToken(cfg.Home, cfg.DashboardAuth)`.
- [ ] **Step 4: Re-run** — same command. Expected: PASS.
- [ ] **Step 5: Commit** — `git add internal/daemon && git commit -m "feat(daemon): require dashboard token only when enabled"`.

---

### Task 3: Make `devstack ui` token-aware

**Files:**
- Modify: `internal/cli/root.go`
- Test: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: `config.Config.DashboardAuth`.

- [ ] **Step 1: Write the failing test** — add:

```go
func TestCLIUIOmitsTokenWhenDashboardAuthDisabled(t *testing.T) {
	useTestSeams(t)
	var opened []string
	oldOpen := openBrowser
	openBrowser = func(url string) error {
		opened = append(opened, url)
		return nil
	}
	t.Cleanup(func() { openBrowser = oldOpen })

	home := t.TempDir()
	if err := securestore.SaveAt(home, "api-token", []byte("0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	runCLI(t, &fakeClient{}, "ui", "--home", home)
	if !reflect.DeepEqual(opened, []string{dashboardURL}) {
		t.Fatalf("opened = %v, want [%s]", opened, dashboardURL)
	}
}
```

Update `TestCLIUIOpensDashboardWithInstallToken` to enable the setting before `runCLI`:

```go
cfg, err := config.Load(home)
if err != nil {
	t.Fatal(err)
}
cfg.DashboardAuth = true
if err := cfg.Save(); err != nil {
	t.Fatal(err)
}
```

- [ ] **Step 2: Run it and watch the new test fail** — `go test ./internal/cli -run TestCLIUIO`. Expected: the new test fails because the fragment is still appended.
- [ ] **Step 3: Implement** — wrap the token load in `if cfg.DashboardAuth { ... }`.
- [ ] **Step 4: Re-run** — same command. Expected: PASS.
- [ ] **Step 5: Commit** — `git add internal/cli && git commit -m "feat(cli): only append dashboard token when auth is enabled"`.

---

### Task 4: Add `init --dashboard-auth`

**Files:**
- Modify: `internal/cli/init.go`
- Test: `internal/cli/cli_test.go`

**Interfaces:**
- Produces: `devstack init --dashboard-auth[=true|false]` persists the setting and stops a running daemon before `ensureRunning` restarts it.

- [ ] **Step 1: Write the failing test** — add a focused init test using the existing fake-helper pattern:

```go
func TestCLIInitDashboardAuthFlag(t *testing.T) {
	useTestSeams(t)
	helper, socket := startFakeHelper(t)
	home := t.TempDir()
	oldDial := dialHelper
	dialHelper = func(string) (*helperclient.Client, error) { return helperclient.Dial(socket) }
	t.Cleanup(func() { dialHelper = oldDial })
	oldHome := userHomeDir
	userHomeDir = func() (string, error) { return t.TempDir(), nil }
	t.Cleanup(func() { userHomeDir = oldHome })

	stopped := 0
	stopDaemon = func(context.Context, string) (bool, error) { stopped++; return true, nil }

	fake := &fakeClient{doctorReport: doctor.Report{OK: true}}
	runCLI(t, fake, "init", "--home", home, "--helper-socket", socket, "--dashboard-auth", "--json")
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DashboardAuth {
		t.Fatal("DashboardAuth = false after --dashboard-auth")
	}
	if stopped != 1 {
		t.Fatalf("stopDaemon calls = %d, want 1", stopped)
	}

	runCLI(t, fake, "init", "--home", home, "--helper-socket", socket, "--dashboard-auth=false", "--json")
	cfg, err = config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DashboardAuth {
		t.Fatal("DashboardAuth = true after --dashboard-auth=false")
	}
}
```

- [ ] **Step 2: Run it and watch it fail** — `go test ./internal/cli -run TestCLIInitDashboardAuthFlag`. Expected: unknown flag `--dashboard-auth`.
- [ ] **Step 3: Implement** — add `cmd.Flags().Bool("dashboard-auth", false, "...")`; in `runInit`, when `cmd.Flags().Changed("dashboard-auth")`, set and save `cfg.DashboardAuth`; immediately before the `ensureRunning` call, if changed call `stopDaemon(ctx, home)` and propagate errors.
- [ ] **Step 4: Re-run** — same command. Expected: PASS.
- [ ] **Step 5: Commit** — `git add internal/cli && git commit -m "feat(cli): add init --dashboard-auth flag"`.

---

### Task 5: Document and run the gates

**Files:**
- Modify: `docs/install-windows.md`, `docs/architecture.md`

- [ ] **Step 1: Document** — in `docs/install-windows.md`, replace the “API still requires the install token” sentence with the default-off/opt-in behavior and the `devstack init --dashboard-auth` command. In `docs/architecture.md`, note that token auth is opt-in via `dashboard_auth`.
- [ ] **Step 2: Run the gates** — `go test ./...`, `npm --prefix ui test -- --run`, `npm --prefix ui run build`. Expected: all pass.
- [ ] **Step 3: Commit** — `git add docs && git commit -m "docs: document opt-in dashboard auth"`.
