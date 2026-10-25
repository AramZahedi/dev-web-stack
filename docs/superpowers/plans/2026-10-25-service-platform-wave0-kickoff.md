# Service Platform Wave 0 Kickoff

- **Status:** Ready for implementation by an external agent
- **Design:** Approved for implementation (2026-10-25)
- **Start from:** `master` at the commit containing this file; confirm with
  `git rev-parse HEAD`
- **Scope:** Wave 0 foundation only (tasks F1–F6)
- **Execution:** parallel-safe phases; maximum three implementation worktrees
- **Pause point:** after Wave 0 is merged, CI is green, and the handoff is updated

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

## 2. Wave 0 phases

Wave 0 is parallelized only where the file ownership is safe.

### Phase 0A — three parallel worktrees

| Task | Branch | Depends on | Owns |
|---|---|---|---|
| F1 | `feat/services-foundation-f1` | none | root paths/permissions |
| F2 | `feat/services-foundation-f2` | none | service catalog/state + SQLite |
| F4 | `feat/services-foundation-f4` | none | config manifest/conflict engine |

These three must not touch the same files.

### Phase 0B — two parallel worktrees, after 0A merge

| Task | Branch | Depends on | Owns |
|---|---|---|---|
| F3 | `feat/services-foundation-f3` | F2 | Compose/helper control plane |
| F5 | `feat/services-foundation-f5` | F2 | site contract fields/migrations/API types |

F3 and F5 must not edit each other's files.

### Phase 0C — serial, after 0B merge

| Task | Branch | Depends on | Owns |
|---|---|---|---|
| F6 | `feat/services-foundation-f6` | F1, F2, F5 | service API/CLI/UI skeleton |

F6 is serial because it edits the API/CLI/UI hot files after F5.

Wave 1 parallelism starts only after Wave 0 is fully merged:

- Lane A: database services;
- Lane B: web servers/vhosts;
- Lane C: global PHP/Composer/shell/builds.

## 3. Create the foundation integration branch and 0A worktrees

Use the native worktree tool if available; otherwise use git. Start from clean `master`.

```sh
git fetch origin
git checkout master
git pull --ff-only
git branch integration/services-foundation master
git worktree add .worktrees/services-foundation-f1 \
  -b feat/services-foundation-f1 integration/services-foundation
git worktree add .worktrees/services-foundation-f2 \
  -b feat/services-foundation-f2 integration/services-foundation
git worktree add .worktrees/services-foundation-f4 \
  -b feat/services-foundation-f4 integration/services-foundation
```

In each worktree:

```sh
go mod download
go test ./... -count=1
```

A failing baseline is a stop condition: report it before changing code.

The `.worktrees/` directory must be ignored by `.gitignore`; verify with
`git check-ignore -q .worktrees`.

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

Tasks in the same phase may be implemented and reviewed concurrently. Do not merge until
each task’s review is clean.

## 5. Merge queue and phase transitions

### 0A merge

Merge completed 0A tasks into `integration/services-foundation` in this order:

```text
F1 → F2 → F4
```

After each merge:

```sh
go test ./internal/config ./internal/services ./internal/store ./internal/serviceconfig -count=1
```

After all three:

```sh
go test ./... -count=1
```

If a task conflicts with an earlier task's owned files, stop: the file-ownership boundary
was violated. Resolve only in the integration branch and record the ruling.

### 0B merge

Create F3/F5 worktrees from the updated integration tip:

```sh
git worktree add .worktrees/services-foundation-f3 \
  -b feat/services-foundation-f3 integration/services-foundation
git worktree add .worktrees/services-foundation-f5 \
  -b feat/services-foundation-f5 integration/services-foundation
```

Run their tests, review, and merge:

```text
F3 → F5
```

Then run:

```sh
go test ./... -count=1
```

### 0C merge

Create F6 from the merged 0B tip, implement/review/merge it. Then run the full Wave 0
checkpoint.

## 6. Wave 0 integration checkpoint

After F1–F6 are merged into `integration/services-foundation`:

- [ ] `go vet ./...`
- [ ] `go test ./... -count=1`
- [ ] `go test ./... -race -count=1`
- [ ] UI check/unit/build
- [ ] `make build`
- [ ] shell syntax and shellcheck
- [ ] ensure no service/site data, `.worktrees`, certificates, or runtime archives are
  staged
- [ ] merge the foundation integration branch to `master`
- [ ] push `master`
- [ ] wait for CI green
- [ ] update `docs/handoff.md` with the merge SHA and CI run
- [ ] remove all Wave 0 worktrees and branches

Do not start Wave 1 in this execution. **Pause and report after Wave 0.**

## 7. Wave ledger

Keep a progress file outside git, for example:

```text
.superpowers/sdd/services-wave0/progress.md
```

Record:

```text
Phase 0A
Task F1: base <sha>, head <sha>, review clean
Task F2: base <sha>, head <sha>, review clean
Task F4: base <sha>, head <sha>, review clean
0A merge: <sha>

Phase 0B
Task F3: base <sha>, head <sha>, review clean
Task F5: base <sha>, head <sha>, review clean
0B merge: <sha>

Phase 0C
Task F6: base <sha>, head <sha>, review clean

Wave 0 checkpoint: <commands/results>
Merge: integration -> master <sha>
CI: <run id>
```

If context is lost, trust the ledger and `git log` over recollection.

## 8. Stop conditions

Stop and report before proceeding if:

- baseline tests fail;
- a task’s specified interface cannot be implemented without changing the design;
- the design/spec is internally contradictory;
- a required secret/image digest/checksum would have to be invented;
- a destructive or host-wide operation is required in Wave 0 (none should be);
- the agent cannot obtain any review pass while the task contains critical security or
  port-ownership logic;
- a merge conflict violates the file-ownership boundaries in the plans.

## 9. Definition of done

Wave 0 is done only when:

- F1–F6 are merged;
- every task has a clean review;
- the Wave 0 integration checkpoint is green;
- `master` CI is green at the merge SHA;
- `docs/handoff.md` records the merge SHA/CI;
- no Wave 1 feature code exists yet;
- no service containers or host tuning were installed;
- all Wave 0 worktrees are removed.

Then **pause and report** to the user. Do not proceed to Wave 1 without explicit approval;
the next step is a review of Wave 0 and a detailed plan for Wave 1.

## 10. What not to do

- Do not implement databases, web-server containers, phpMyAdmin, performance tuning,
  global PHP/Composer, `.htaccess` behavior, or dashboard build/terminal features in
  Wave 0. Those belong to Waves 1–2.
- Do not add production service image digests or catalog entries without real manifests.
- Do not recreate or edit the retired standalone devstack Docker image.
- Do not start Windows/WSL, arm64, signed packages, auto-update, or M4 work.
- Do not move `~/.devstack` to `/data/devstack` automatically.
- Do not commit runtime archives, certificates, secrets, service data, or site data.

## 11. Handoff back

Report:

```text
Wave 0 status: complete | blocked
Merge SHA:
CI run:
Phase 0A commits:
Phase 0B commits:
Phase 0C commits:
Review status:
Known issues:
Worktrees removed: yes/no
```

After Wave 0 is merged and this report is delivered, the controller will review the
foundation and plan Wave 1 against the frozen interfaces.
