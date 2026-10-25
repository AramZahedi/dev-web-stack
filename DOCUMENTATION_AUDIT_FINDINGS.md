# Documentation Audit Findings and Remediation Guide

> **Resolution (2026-10-25):** All ten findings were verified against the
> repository and fixed in the documentation. One wording correction was made:
> the helper ownership check is `validateBackendOwnership` (not
> `validateBackendPath`). The audit file is retained as the review record; the
> corrected docs are the source of truth.

- **Date:** 2026-10-25
- **Audit Target:** All documentation and specification markdown (`.md`) files in the repository
- **Baseline Commit:** `41d6b12` on `master`
- **Scope:** 37 markdown files across `build/`, `design-system/`, `docs/`, and repo root
- **Purpose:** Provide an exhaustive, actionable inventory of documentation defects, stale status headers, outdated assumptions, and omissions so they can be fixed cleanly before beginning implementation plans.

---

## 1. Summary of Findings

| ID | Category | Severity | File | Affected Lines | Summary |
|---|---|---|---|---|---|
| **DOC-01** | Artifact Gate | High | [`build/php/README.md`](build/php/README.md) | Lines 7–12 | Stale artifact gate callout claims production catalog contains no PHP entries |
| **DOC-02** | Artifact Gate | High | [`docs/vhosts.md`](docs/vhosts.md) | Lines 14–17, 95–96 | References an active PHP production catalog gate that is already resolved |
| **DOC-03** | Artifact Gate | High | [`docs/sites.md`](docs/sites.md) | Lines 178–180 | Claims production catalog is gated until release artifacts are published |
| **DOC-04** | Artifact Gate | Medium | [`README.md`](README.md) | Lines 124–125 | States PHP catalog remains gated, directly contradicting earlier sections in README and `docs/php.md` |
| **DOC-05** | CLI Options | Medium | [`docs/sites.md`](docs/sites.md) | Lines 116–130 | Options table omits `--kind php`, `--php`, `--php-build`, `--front-controller`, and lacks a PHP site example |
| **DOC-06** | Security Rule | Medium | [`docs/sites.md`](docs/sites.md) | Line 123 | Omits containment rule: absolute `--document-root` must reside inside the site's project directory |
| **DOC-07** | Spec Status | Low | [`docs/superpowers/specs/2026-10-22-dev-web-stack-m1-design.md`](docs/superpowers/specs/2026-10-22-dev-web-stack-m1-design.md) | Lines 3, 6, 13–14 | Status header still reads "Draft for review"; lists Windows 11/WSL2 as active target without noting deferral |
| **DOC-08** | Spec Status | Low | [`docs/superpowers/specs/2026-10-22-external-vhosts-design.md`](docs/superpowers/specs/2026-10-22-external-vhosts-design.md) | Line 3 | Status header reads "Approved for planning" although external vhost support is fully implemented |
| **DOC-09** | Plan Status | Low | [`docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-php.md`](docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-php.md) | Line 15 | Lacks completion note indicating the real 8.2/8.5 side-by-side gate was completed in Plan 4 |
| **DOC-10** | Architecture Drift | Low | [`docs/architecture.md`](docs/architecture.md) | Lines 241–256, 308–321 | Omits post-M1 audit remediations (session requirement on `/api/v1/fs`, `Proxy` header filtering, helper path ownership) and CLI `site` aliases |

---

## 2. Detailed Findings and Remediations

### DOC-01: Stale PHP Artifact Gate Warning in `build/php/README.md`

- **File:** [`build/php/README.md`](build/php/README.md#L7-L12)
- **Lines:** 7–12
- **Current Content:**
  ```markdown
  > **Artifact gate:** the production catalog contains no PHP entries. A PHP build
  > becomes installable only after CI publishes the four checksummed archives, a
  > release job uploads them, and a subsequent release embeds their entries in
  > `internal/catalog/catalog.json`. Until then `devstack runtime install` has no
  > PHP build to install. CI uploads `php-catalog.json` as an artifact; merging it
  > is a deliberate release step.
  ```
- **Defect:** This callout was written prior to release `php-runtimes-2026-10-25`. The production catalog [`internal/catalog/catalog.json`](internal/catalog/catalog.json) now contains entries for `php@8.2.35-r1`, `php@8.3.36-r1`, `php@8.4.27-r1`, and `php@8.5.12-r1`.
- **Evidence:** [`docs/php.md#L7-L13`](docs/php.md#L7-L13) ("Artifact gate: resolved.") and [`docs/release.md#L186-L200`](docs/release.md#L186-L200).
- **Remediation:** Update the callout box to acknowledge that the initial catalog is populated and explain that the gate applies to new PHP releases:
  ```markdown
  > **Artifact gate: resolved for initial release.** The production catalog shipped in
  > `devstack` contains PHP 8.2.35, 8.3.36, 8.4.27, and 8.5.12 builds from the
  > `php-runtimes-2026-10-25` release. A newly published PHP version becomes
  > installable only after CI publishes the checksummed archive, a release job
  > uploads it, and a subsequent release embeds its entry in
  > `internal/catalog/catalog.json`. CI uploads `php-catalog.json` as an artifact;
  > merging it via `cmd/catalogmerge` is a deliberate release step.
  ```

---

### DOC-02: Stale PHP Catalog Gate Warnings in `docs/vhosts.md`

- **File:** [`docs/vhosts.md`](docs/vhosts.md#L14-L17) and [`docs/vhosts.md#L95-L96`](docs/vhosts.md#L95-L96)
- **Lines:** 14–17, 95–96
- **Current Content (Lines 14–17):**
  ```markdown
  External serving is Linux only. Static, Node, and PHP sites are supported. A PHP
  site additionally needs an installed PHP build; the production catalog is gated
  until release artifacts exist (see [PHP](php.md)). There is no per-site serving
  mode and nginx and Apache cannot run at the same time on ports 80 and 443.
  ```
- **Current Content (Lines 95–96):**
  ```markdown
  - A PHP site needs an installed PHP build before its vhost can be published.
    The production artifact gate is documented in [PHP](php.md).
  ```
- **Defect:** States that the production catalog is gated until release artifacts exist.
- **Evidence:** Release artifacts exist in `php-runtimes-2026-10-25`; catalog is populated.
- **Remediation:**
  - Replace lines 14–17 with:
    ```markdown
    External serving is Linux only. Static, Node, and PHP sites are supported. A PHP
    site additionally needs an installed PHP build from the runtime catalog (see
    [PHP](php.md)). There is no per-site serving mode and nginx and Apache cannot
    run at the same time on ports 80 and 443.
    ```
  - Replace lines 95–96 with:
    ```markdown
    - A PHP site needs an installed PHP build before its vhost can be published
      (see [PHP](php.md)).
    ```

---

### DOC-03: Stale PHP Artifact Gate Warning in `docs/sites.md`

- **File:** [`docs/sites.md`](docs/sites.md#L178-L180)
- **Lines:** 178–180
- **Current Content:**
  ```markdown
  - PHP sites need an installed PHP build; the production catalog is gated until
    release artifacts are published (see [PHP](php.md)).
  ```
- **Defect:** Stale warning regarding an active artifact gate.
- **Remediation:** Replace lines 178–180 with:
  ```markdown
  - PHP sites need an installed PHP build from the runtime catalog (see
    [PHP](php.md)).
  ```

---

### DOC-04: Contradictory PHP Catalog Statement in `README.md`

- **File:** [`README.md`](README.md#L124-L125)
- **Lines:** 124–125
- **Current Content:**
  ```markdown
  The PHP build is a real source build and runs in the dedicated
  [PHP workflow](.github/workflows/php.yml) on `ubuntu-22.04`; the local
  development environment does not have the PHP build dependencies. The PHP
  catalog remains gated as described in [docs/php.md](docs/php.md).
  ```
- **Defect:** Inconsistent with earlier sections in `README.md` (lines 21–24 and lines 165–172), which confirm that PHP 8.2–8.5 builds are installable from the shipped catalog, and contradicts `docs/php.md:7` ("Artifact gate: resolved.").
- **Remediation:** Replace lines 124–125 with:
  ```markdown
  The PHP build is a real source build and runs in the dedicated
  [PHP workflow](.github/workflows/php.yml) on `ubuntu-22.04`; the local
  development environment does not have the PHP build dependencies. The production
  catalog ships with PHP 8.2–8.5 builds; newly published versions follow the gated
  merge procedure described in [docs/php.md](docs/php.md).
  ```

---

### DOC-05: Missing PHP CLI Options and Examples in `docs/sites.md`

- **File:** [`docs/sites.md`](docs/sites.md#L114-L130)
- **Lines:** 114–130
- **Current Content (Line 120):**
  ```markdown
  | `--kind` | `static` (default) or `node` |
  ```
- **Defect:**
  1. `--kind` options omit `php`.
  2. The table omits flags supported by `site add` in [`internal/cli/site.go#L76-L92`](internal/cli/site.go):
     - `--php`: PHP version line (e.g. `8.4`).
     - `--php-build`: exact PHP build ID (e.g. `php@8.4.27-r1`).
     - `--front-controller`: route requests to the site's `index.php`.
  3. `docs/sites.md` has "Add a static site" (§2) and "Add a Node site" (§3), but no "Add a PHP site" example section.
- **Remediation:**
  1. Add an "Add a PHP site" section:
     ```markdown
     ## Add a PHP site

     PHP sites run on per-site `php-fpm` masters. You must specify the PHP line
     (e.g. `8.4`):

     ```sh
     mkdir -p "$HOME/projects/mysite"
     echo '<?php phpinfo();' > "$HOME/projects/mysite/index.php"

     devstack site add \
       --name mysite \
       --kind php \
       --path "$HOME/projects/mysite" \
       --php 8.4 \
       --front-controller
     ```

     Use `--php-build` to pin an exact build (e.g. `php@8.4.27-r1`), or `--php` to
     select the newest installed build on that line. See [PHP](php.md) for full
     details on `php.ini` configuration and extension toggles.
     ```
  2. Update the options table:
     ```markdown
     | Flag | Meaning |
     |---|---|
     | `--name` | Site name; the domain is `<name>.test` |
     | `--path` | Absolute project directory |
     | `--kind` | `static` (default), `php`, or `node` |
     | `--php` | PHP version line, for example `8.4` (PHP sites only) |
     | `--php-build` | Exact PHP build id, for example `php@8.4.27-r1` (PHP sites only) |
     | `--front-controller` | Route non-file requests to `index.php` (PHP sites only) |
     | `--node` | Node version or catalog prefix (Node sites only) |
     | `--command` | Start command (Node sites only) |
     | `--document-root` | Static or PHP document root relative to `--path`, or absolute within `--path` |
     | `--www` | Also serve `www.<name>.test` |
     | `--no-https` | Serve HTTP only |
     | `--force-https` | Redirect HTTP to HTTPS |
     | `--spa` | Serve `index.html` for non-file paths |
     | `--env KEY=VALUE` | Site environment variable; repeatable |
     | `--yes` | Install a missing runtime without prompting |
     ```

---

### DOC-06: Missing Document Root Containment Constraint in `docs/sites.md`

- **File:** [`docs/sites.md`](docs/sites.md#L123)
- **Line:** 123
- **Current Content:**
  ```markdown
  | `--document-root` | Static document root relative to `--path`, or absolute |
  ```
- **Defect:** In commit `a19b5be` (audit fix for CRIT-01 in `internal/sites/service.go`), validation logic was added to enforce that any absolute `--document-root` must remain inside the site's project path (`--path`). An absolute path outside the project directory fails with `INVALID_DOCUMENT_ROOT`.
- **Remediation:** Update the table description and limitations section to explicitly note:
  ```markdown
  | `--document-root` | Document root relative to `--path`, or absolute within `--path` |
  ```

---

### DOC-07: Outdated Status and Platform Scope in M1 Design Spec

- **File:** [`docs/superpowers/specs/2026-10-22-dev-web-stack-m1-design.md`](docs/superpowers/specs/2026-10-22-dev-web-stack-m1-design.md)
- **Lines:** 3, 6, 13–14
- **Current Content:**
  ```markdown
  - **Status:** Draft for review
  - **Date:** 2026-10-22
  - **Scope:** Milestone 1 only
  - **Target platforms:** systemd-based Linux x86_64 (glibc 2.35+) and Windows 11 x86_64 with WSL2
  ```
- **Defect:**
  1. Status is still listed as "Draft for review", despite M1 being finalized, approved, implemented, and released in `v0.1.0`.
  2. Windows 11 with WSL2 was explicitly deferred to Plan 3 by user request; M1 was delivered as Linux-only.
- **Remediation:**
  - Update line 3 to:
    ```markdown
    - **Status:** Implemented in v0.1.0 (Linux; Windows/WSL deferred to Plan 3)
    ```
  - Update line 6 to:
    ```markdown
    - **Target platforms:** systemd-based Linux x86_64 (glibc 2.35+); Windows 11 with WSL2 deferred to Plan 3
    ```

---

### DOC-08: Outdated Status in External Vhosts Design Spec

- **File:** [`docs/superpowers/specs/2026-10-22-external-vhosts-design.md`](docs/superpowers/specs/2026-10-22-external-vhosts-design.md#L3)
- **Line:** 3
- **Current Content:**
  ```markdown
  - **Status:** Approved for planning
  ```
- **Defect:** Outdated status header. External nginx and Apache vhost management was implemented in Plan 1 and Plan 2 and released in `v0.1.0`.
- **Remediation:** Update line 3 to:
  ```markdown
  - **Status:** Implemented in v0.1.0
  ```

---

### DOC-09: Missing Completion Note in Plan 2 (PHP)

- **File:** [`docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-php.md`](docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-php.md#L15)
- **Line:** 15
- **Current Content:**
  ```markdown
  > **Implementation status (2026-10-24):** Tasks 1–12 are complete and task-reviewed... The real 8.2/8.5 side-by-side and production catalog publication remain **BLOCKED** on published Task 11 CI artifacts.
  ```
- **Defect:** Unlike Plan 4 ([`docs/superpowers/plans/2026-10-24-dev-web-stack-plan-4-release.md#L3-L9`](docs/superpowers/plans/2026-10-24-dev-web-stack-plan-4-release.md#L3-L9)), Plan 2 lacks a brief completion note stating that the real side-by-side gate and catalog merge were completed during Plan 4 with the release of `php-runtimes-2026-10-25`.
- **Remediation:** Add a completion note above or within line 15:
  ```markdown
  > **Completion note (2026-10-25):** The real 8.2/8.5 side-by-side gate and
  > catalog publication were completed in Plan 4 following the publication of the
  > `php-runtimes-2026-10-25` release archives.
  ```

---

### DOC-10: Post-M1 Security & Reliability Remediations Missing from Architecture Guide

- **File:** [`docs/architecture.md`](docs/architecture.md)
- **Lines:** 241–256 (§8 CLI surface), 308–321 (§11 Security model)
- **Defect:**
  1. In §8 CLI surface, `devstack site` aliases (`devstack site php`, `devstack site ini`, `devstack site ext`) implemented in `internal/cli/site.go` are omitted from the command list.
  2. In §11 Security Model, key audit remediations committed in `c8eef74` and `a19b5be` are omitted:
     - `/api/v1/fs` directory browsing requires an authenticated dashboard session cookie, fails closed if user home directory cannot be determined, and confines browsing within the user's home directory.
     - Reverse proxy strips incoming `Proxy` headers to prevent FastCGI/CGI httpoxy vulnerabilities.
     - Helper `validateBackendPath` validates that the site project directory is owned by the calling `allow_uid`, rejecting root-owned or non-user paths during vhost generation.
- **Remediation:**
  1. Update §8 CLI surface:
     ```text
     devstack site add|list|show|start|stop|restart|remove|logs|open|php|ini|ext
     ```
  2. Add the following bullets to §11 Security model:
     ```markdown
     - `/api/v1/fs` directory browsing requires an active dashboard session, fails closed on empty home directory, and is confined to the user's home directory.
     - Client `Proxy` headers are stripped by the reverse proxy before FastCGI/CGI dispatch.
     - Helper validates project path UID ownership against `allow_uid` before installing native vhosts.
     ```

---

## 3. Verification Checklist for Fixing Agent

After applying the fixes above, the implementing agent should run the following verification steps:

```sh
# 1. Verify Go build and full test suite passes
make test
go test ./... -race -count=1

# 2. Verify all relative and file:// links resolve cleanly
python3 - << 'EOF'
import re
from pathlib import Path

repo_root = Path(".")
md_files = [f for f in repo_root.glob("**/*.md") if "node_modules" not in str(f) and ".superpowers" not in str(f)]
broken = []
link_pattern = re.compile(r'\[([^\]]+)\]\(([^)]+)\)')

for md_file in md_files:
    content = md_file.read_text(encoding="utf-8", errors="ignore")
    content_no_code = re.sub(r'```.*?```', '', content, flags=re.DOTALL)
    for match in link_pattern.finditer(content_no_code):
        text, target = match.groups()
        target = target.strip()
        if target.startswith(('http://', 'https://', 'mailto:', '#', 'tel:')):
            continue
        if target.startswith('file://'):
            file_path = target.replace('file://', '').split('#')[0]
            if not Path(file_path).exists():
                broken.append((str(md_file), target, text))
            continue
        base_target = target.split('#')[0]
        if not base_target:
            continue
        target_path = (md_file.parent / base_target).resolve()
        if not target_path.exists():
            broken.append((str(md_file), target, text))

if broken:
    print(f"FAILED: {len(broken)} broken link(s)")
    for src, tgt, txt in broken:
        print(f"  {src} -> {tgt} ({txt})")
    exit(1)
print("SUCCESS: All links valid")
EOF

# 3. Check git diff for intended changes only
git diff --stat
```
