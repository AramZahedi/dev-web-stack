# Service Platform Wave 0 Kickoff

- **Status:** Ready for implementation by an external agent
- **Design:** Approved for implementation (2026-10-25)
- **Start from:** `master` at the commit containing this file; confirm with
  `git rev-parse HEAD`
- **Scope:** Wave 0 foundation only (tasks F1–F6)
- **Execution:** **serial**, one worktree. Parallel lanes start in Wave 1.

## 1. Read first, in this order

1. [Program plan](2026-10-25-service-platform-program.md) — worktree/merge mechanism,
   waves, file ownership, review protocol.
2. [Foundation plan](2026-10-25-service-platform-foundation.md) — exact F1–F6 tasks,
   files, interfaces, tests, commit commands.
3. [Design spec](../specs/2026-10-25-service-containers-and-performance-design.md) —
   authoritative product/architecture decisions.
4. [Handoff](../../handoff.md), [architecture](../../architecture.md),
   [requirements](../../requirements.md), [decisions](../../decisions.md).

Do not implement from this kickoff alone. The foundation plan is the task authority over
this summary; the design spec is the product authority over both.

## 2. Wave 0 is intentionally serial

Do **not** create three parallel worktrees for Wave 0. The foundation changes shared
files and defines contracts used by every later lane. Parallelizing it would create the
exact hot-file conflicts the program is designed to avoid.

Wave 0 tasks:

| Task | Deliverable |
|---|---|
| F1 | Root layout and permissions |
| F2 | Service catalog/state and SQLite registry |
| F3 | Compose rendering and fixed helper/Docker control |
| F4 | Config manifest/conflict/diff/backup engine |
| F5 | Site front-controller/`.htaccess`/SPA/build contract fields |
| F6 | Read-only Services API/CLI/UI skeleton |

Parallelism starts at Wave 1:

- Lane A: database services;
- Lane B: web servers/vhosts;
- Lane C: global PHP/Composer/shell/builds.

## 3. Create the Wave 0 worktree

Use the native worktree tool if available. Otherwise use git. Start from clean `master`.

```sh
git fetch origin
git checkout master
git pull --ff-only
git worktree add .worktrees/services-foundation-wave0 -b feat/services-foundation-wave0
cd .worktrees/services-foundation-wave0
go mod download
go test ./... -count=1
```

A failing baseline is a stop condition: report it before changing code.

The worktree directory is expected to be ignored by `.gitignore`; F1 adds
`.worktrees/` if it is not already there.

## 4. Task execution contract

For each F1–F6:

1. Read only that task plus the Global Constraints and Review Focus in the foundation
   plan.
2. Write the failing test(s) exactly as specified.
3. Run the test and capture the expected failure.
4. Implement the minimal production change.
5. Run focused tests, then the task’s stated broader tests.
6. Self-review the diff against the task and spec.
7. Obtain an independent task review if the agent platform supports fresh reviewers;
   otherwise perform a separate fresh-context review pass. Do not accept open
   Critical/Important findings.
8. Fix and re-review. Do not skip the loop.
9. Commit with the exact commit instructions in the task.
10. Update the plan checkbox and the wave ledger with base/head commits and review result.

Do not combine tasks into one commit. Do not start F(n+1) before F(n) is reviewed and
committed.

## 5. Wave ledger

Keep a progress file outside git, for example:

```text
.superpowers/sdd/services-wave0/progress.md
```

Record:

```text
Task F1: base <sha>, head <sha>, review clean
Task F2: base <sha>, head <sha>, review clean
...
Wave 0 checkpoint: <commands/results>
Merge: integration -> master <sha>
CI: <run id>
```

If context is lost, trust the ledger and `git log` over recollection.

## 6. Integration and merge

After F1–F6 are committed on the wave branch:

1. Rebase onto current `master`.
2. Run the Wave 0 integration checkpoint from the foundation plan:
   - `go vet ./...`
   - `go test ./... -count=1`
   - `go test ./... -race -count=1`
   - UI check/unit/build
   - `make build`
   - shell syntax/shellcheck
   - ensure no service/site data, `.worktrees`, certificates, or runtime archives are
     staged
3. Fix and re-review any failure.
4. Merge the wave branch to `master` only when the checkpoint is green.
5. Push `master`.
6. Wait for CI green.
7. Update `docs/handoff.md` with the Wave 0 merge SHA and CI run.
8. Remove the worktree and branch after merge.

Do not merge directly to `master` from `git worktree` while another implementation branch
is active. Wave 0 has no other implementation branch.

## 7. Stop conditions

Stop and report before proceeding if:

- baseline tests fail;
- a task’s specified interface cannot be implemented without changing the design;
- the design/spec is internally contradictory;
- a required secret/image digest/checksum would have to be invented;
- a destructive or host-wide operation is required in Wave 0 (none should be);
- the agent cannot obtain any review pass while the task contains critical security or
  port-ownership logic;
- a merge conflict touches a foundation-owned hot file in a way the plan does not resolve.

## 8. Definition of done

Wave 0 is done only when:

- F1–F6 are merged;
- every task has a clean review;
- the Wave 0 integration checkpoint is green;
- `master` CI is green at the merge SHA;
- `docs/handoff.md` records the merge SHA/CI;
- no Wave 1 feature code exists yet;
- no service containers or host tuning were installed;
- the worktree is removed.

## 9. What not to do

- Do not implement databases, web-server containers, phpMyAdmin, performance tuning,
  global PHP/Composer, `.htaccess` behavior, or dashboard build/terminal features in
  Wave 0. Those belong to Waves 1–2.
- Do not add production service image digests or catalog entries without real manifests.
- Do not recreate or edit the retired standalone Docker image. Its `7071/7070` issue is historical.
- Do not start Windows/WSL, arm64, signed packages, auto-update, or M4 work.
- Do not move `~/.devstack` to `/data/devstack` automatically.
- Do not commit runtime archives, certificates, secrets, service data, or site data.

## 10. Handoff back

Report:

```text
Wave 0 status: complete | blocked
Merge SHA:
CI run:
Tasks completed:
Review status:
Known issues:
Files added/changed:
Worktree removed: yes/no
```

After Wave 0 is merged, Wave 1 planning begins against the frozen interfaces. The Wave 1
lanes may then run in parallel worktrees as described in the program plan.
