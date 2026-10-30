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
| **Windows Mode** (`windows`) | Windows NTFS drive | `%USERPROFILE%\.devstack\projects\mysite` or `C:\Users\Aram\...\mysite` | Projects edited with Windows tools that require native NTFS file events / watchers. | Fastest IDE indexing; slightly slower runtime I/O via 9P (`/mnt/c/`). |
| **WSL Mode** (`wsl`) | WSL2 Linux ext4 disk | `\\wsl.localhost\devstack\data\devstack\projects\mysite` | Heavy PHP/Node applications (Laravel, Symfony, Nuxt, Next) with large `vendor/` and `node_modules/`. | Maximum execution speed on native Linux ext4; IDE connects via WSL remote or UNC. |

### Selecting Source Mode

When creating a site via CLI:

```powershell
# Windows source mode (NTFS)
devstack site add --name my-app --path C:\projects\my-app --kind php --source-mode windows

# WSL source mode (ext4)
devstack site add --name fast-app --path \\wsl.localhost\devstack\data\devstack\projects\fast-app --kind php --source-mode wsl
```

You can also toggle or choose the source mode directly in the Add Site and Site Detail views in the web dashboard.

---

## 2. Path Translation Architecture

Paths must be translated dynamically between Windows and Linux formats as
requests cross the process boundaries:

```
┌─────────────────────────────────┐               ┌─────────────────────────────────┐
│          Windows Host           │               │        WSL2 Linux Distro        │
│                                 │               │                                 │
│  C:\projects\my-app             │ ────────────► │  /mnt/c/projects/my-app         │
│  (DOS Drive Path)               │  translation  │  (9P Automount Path)            │
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
- **Directory Traversal:** Any path attempting traversal (`..`) outside the declared site root is immediately rejected with error code `PATH_ESCAPE`.
- **Symlinks and Windows Junctions:** Cross-boundary symbolic links and NTFS directory junctions pointing outside project boundaries are strictly rejected to prevent privilege escalation or data leakage.

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
php C:\projects\my-app\artisan migrate
# Routes inside WSL to:
# /data/devstack/runtimes/php/8.4/bin/php /mnt/c/projects/my-app/artisan migrate
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
