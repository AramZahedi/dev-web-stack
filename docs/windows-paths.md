# Windows & WSL Paths, Translation, and Tooling Shims

devstack supports hybrid Windows/WSL development. A web developer may edit code
using native Windows editors (VS Code, PhpStorm, Sublime Text) while running
runtime engines (PHP, Node, Composer) inside the managed Linux WSL2 environment.

This document describes how devstack manages filesystem boundaries, path
translations, UNC network shares, directory safety, and `.cmd` tooling shims.

---

## 1. Dual Project Source Modes

devstack provides two distinct source modes when adding a site:

| Source Mode | Physical Location | Path Syntax | Recommended For | Performance Characteristics |
|---|---|---|---|---|
| **Windows Mode** (`windows`) | Windows NTFS drive | `%USERPROFILE%\.devstack\projects\mysite` (or the configured projects root) | Projects edited with Windows tools that require native NTFS file events / watchers. | Fastest IDE indexing; slightly slower runtime I/O via 9P (`/mnt/c/`). |
| **WSL Mode** (`wsl`) | WSL2 Linux ext4 disk | `\\wsl.localhost\devstack\data\devstack\projects\mysite` | Heavy PHP/Node applications (Laravel, Symfony, Nuxt, Next) with large `vendor/` and `node_modules/`. | Maximum execution speed on native Linux ext4; IDE connects via WSL remote or UNC. |

### Selecting Source Mode

When creating a site via CLI:

```powershell
# Windows source mode (NTFS)
devstack site add --name my-app --path "$env:USERPROFILE\.devstack\projects\my-app" --kind php --source-mode windows

# WSL source mode (ext4)
devstack site add --name fast-app --path \\wsl.localhost\devstack\data\devstack\projects\fast-app --kind php --source-mode wsl
```

You can also toggle or choose the source mode directly in the Add Site and Site Detail views in the web dashboard.

### Managed projects root (hard restriction)

Every site path must be a **strict descendant** of the managed projects root for
its source mode. The root itself is not a valid site path.

| Mode | Root |
|---|---|
| Windows (`windows`) | configured `projects_root`, default `%USERPROFILE%\.devstack\projects` |
| WSL (`wsl`) | `/data/devstack/projects` in the managed distro (`\\wsl.localhost\devstack\data\devstack\projects`) |
| Linux local (`local`) | configured `projects_root`, default `~/.devstack/projects` |

Containment is component-aware: `projects-old\mysite` is not inside `projects`.
Windows comparisons are case-insensitive. Existing reparse points are resolved
and rechecked, so a link inside the root that escapes it is rejected.

The rule is enforced when a site is created, when a source-mode copy destination
is selected, and when a site is started or reconciled. Existing sites outside the
root are not deleted; they are blocked from starting and reported by
`devstack doctor`. The folder browser and mkdir endpoint are confined to the same
roots. Browsing the root itself is allowed, but selecting it as a site folder is
not. Document roots stay relative to the site folder.

### The `C:\devstack` links are navigation only

The Windows installer creates a real `C:\devstack` folder with four links:

| Link | Type | Target |
|---|---|---|
| `C:\devstack\Windows-Install` | junction | `%USERPROFILE%\.devstack` |
| `C:\devstack\Windows-Projects` | junction | `%USERPROFILE%\.devstack\projects` |
| `C:\devstack\WSL-Install` | directory symlink | `\\wsl.localhost\devstack\data\devstack` |
| `C:\devstack\WSL-Projects` | directory symlink | `\\wsl.localhost\devstack\data\devstack\projects` |

They are live views, not copies. Deleting a link removes only the link; the
target and its data remain. The WSL links require the managed `devstack`
distribution. `C:\devstack\README.txt` documents the exact targets and repair
steps.

The links are never valid site paths. Use the canonical target instead:
`C:\devstack\Windows-Projects\mysite` is rejected, while
`%USERPROFILE%\.devstack\projects\mysite` is accepted. Repair missing or stale
links with an elevated `.\install.ps1 -Upgrade`.

---

## 2. Path Translation Architecture

Paths must be translated dynamically between Windows and Linux formats as
requests cross the process boundaries:

```
┌─────────────────────────────────┐               ┌─────────────────────────────────┐
│          Windows Host           │               │        WSL2 Linux Distro        │
│                                 │               │                                 │
│  C:\Users\Aram\.devstack\       │ ────────────► │  /mnt/c/Users/Aram/.devstack/   │
│  projects\my-app                │  translation  │  projects/my-app                │
│  (DOS Drive Path)               │               │  (9P Automount Path)            │
│                                 │               │                                 │
│  \\wsl.localhost\devstack\data\ │ ────────────► │  /data/devstack/projects/app    │
│  devstack\projects\app (UNC)    │  translation  │  (Native Linux ext4 Path)       │
└─────────────────────────────────┘               └─────────────────────────────────┘
```

### Windows Drive to 9P Automount

Windows drive paths (`C:\...`) are converted to WSL mount points (`/mnt/c/...`) using the `internal/wslpath` package:
1. Replaces backslashes `\` with forward slashes `/`.
2. Extracts the drive letter (e.g. `C:`), lowercases it, and prefixes it with `/mnt/`.
3. Handles directory whitespace without fragile shell escaping.

### UNC Paths to Native Linux Paths

Paths beginning with `\\wsl.localhost\devstack\` or legacy `\\wsl$\devstack\` map directly to the root of the managed distro:
- `\\wsl.localhost\devstack\data\devstack\sites\mysite` becomes `/data/devstack/sites/mysite`.
- Backslashes are normalized to forward slashes.
- Windows drive letters and share names are stripped.

---

## 3. Path Safety & Escapes (Decision S65)

Because UNC shares can present security ambiguity, devstack **never relies on Windows-side UNC checks as its sole security boundary**.

All filesystem access, traversal, and validation is verified on the agent side using POSIX primitives:
- **Canonicalization:** The agent resolves real paths using `realpath(3)` and `lstat(2)` to verify the canonical target.
- **Directory Traversal:** Any path attempting traversal (`..`) outside the declared site root or managed projects root is immediately rejected with error code `PATH_ESCAPE`.
- **Symlinks and Windows Junctions:** Cross-boundary symbolic links and NTFS directory junctions pointing outside project boundaries are strictly rejected to prevent privilege escalation or data leakage.

The managed projects root is a hard boundary, not a suggested default. A site
path is accepted only when it is a strict descendant of the root. The root
itself, a sibling such as `projects-old`, a `..` segment, or a link that resolves
outside the root is rejected before the backend is started. This is enforced at
create time, at the source-mode copy destination, and on every start or
reconcile.

### Safe Source Mode Migration

When switching a site between `windows` and `wsl` modes:
1. The site must first be stopped.
2. The agent creates a SHA-256 manifest of the source files.
3. The directory tree is copied using an atomic tar pipeline preserving file permissions.
4. The destination is verified against the SHA-256 manifest.
5. If verification fails, changes are automatically rolled back to ensure zero data loss.
6. Original source files are retained by default unless `--discard-source` is explicitly requested.

---

## 4. Windows Tooling Shims (`.cmd` Wrappers)

To provide developers with a seamless command-line experience without needing native Windows PHP or Node installations, devstack installs global wrapper scripts into `%USERPROFILE%\.devstack\bin\`:

```
%USERPROFILE%\.devstack\bin\php.cmd
%USERPROFILE%\.devstack\bin\composer.cmd
```

Because `%USERPROFILE%\.devstack\bin` is registered in the user or system `PATH`, you can invoke `php` or `composer` directly in PowerShell, Command Prompt, or within IDE terminals (PhpStorm, VS Code).

### How the Shims Work

The `.cmd` shims:
1. Intercept command-line arguments.
2. Convert any argument formatted as a Windows path (`C:\...`) into its corresponding WSL mount path (`/mnt/c/...`).
3. Invoke the managed distro using `wsl.exe -d devstack -- ...`.
4. Preserve exit codes, standard output, and standard error streams.

Example invocation:

```powershell
# Executed on Windows:
php -v
# Routes inside WSL to:
# /data/devstack/runtimes/php/8.4/bin/php -v

# Executed with file paths:
php C:\Users\Aram\.devstack\projects\my-app\artisan migrate
# Routes inside WSL to:
# /data/devstack/runtimes/php/8.4/bin/php /mnt/c/Users/Aram/.devstack/projects/my-app/artisan migrate
```

---

## 5. The No-Dot File Policy (Decision S70)

In Windows environments, dot-prefixed filenames (such as `.devstack` or `.config`) can cause friction with file explorers, backup software, and PowerShell commandlets.

Under Decision S70, **no devstack-created Windows file or directory below the install root may start with a dot**. The install root itself is the documented exception, because Windows installs live under `%USERPROFILE%\.devstack`:
- Database: `%USERPROFILE%\.devstack\state.db` (never `.devstack.db`)
- Manifests: `%USERPROFILE%\.devstack\system\install-manifest.json` (never `.manifest`)
- User configuration: `%USERPROFILE%\.devstack\config.yaml` (never `.config.yaml`)

> [!NOTE]
> Project-owned dot files (such as `.git`, `.env`, `.htaccess`, `.gitignore`) residing inside user code repositories are fully supported and untouched.
