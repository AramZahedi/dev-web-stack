#!/usr/bin/env bash
# Static contract test for the release workflow and its GoReleaser config.
#
# This test never calls GitHub or GoReleaser. It asserts the checked-in YAML
# keeps the release tag-only, least-privilege, Linux-only, and free of the
# deferred agent/platform builds, and that CI still runs the packaging tests.
set -Eeuo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
WORKFLOW="${REPO_ROOT}/.github/workflows/release.yml"
CI_WORKFLOW="${REPO_ROOT}/.github/workflows/ci.yml"
GORELEASER="${REPO_ROOT}/.goreleaser.yaml"

FAILURES=0

pass() {
	printf 'workflow-test: ok: %s\n' "$*"
}

fail() {
	printf 'workflow-test: FAIL: %s\n' "$*" >&2
	FAILURES=$((FAILURES + 1))
}

assert_file() {
	local file="$1"
	if [ -f "${file}" ]; then
		pass "exists: ${file#"${REPO_ROOT}/"}"
	else
		fail "missing ${file}"
	fi
}

# assert_fixed FILE TEXT LABEL checks for a literal string.
assert_fixed() {
	local file="$1" text="$2" label="${3:-$2}"
	if grep -Fq -- "${text}" "${file}"; then
		pass "${label}"
	else
		fail "${label} (missing: ${text})"
	fi
}

# assert_regex FILE PATTERN LABEL checks a regular expression.
assert_regex() {
	local file="$1" pattern="$2" label="${3:-$2}"
	if grep -Eq -- "${pattern}" "${file}"; then
		pass "${label}"
	else
		fail "${label} (missing pattern: ${pattern})"
	fi
}

# assert_no_active FILE PATTERN LABEL ignores YAML comments so deferred-work
# notes cannot satisfy or break a check about active configuration.
assert_no_active() {
	local file="$1" pattern="$2" label="${3:-$2}"
	if grep -vE '^[[:space:]]*#' "${file}" | grep -Eiq -- "${pattern}"; then
		fail "${label} (found active pattern: ${pattern})"
	else
		pass "${label}"
	fi
}

assert_file "${WORKFLOW}"
assert_file "${GORELEASER}"
assert_file "${CI_WORKFLOW}"

# --- Trigger contract: tags only, plus an explicit manual tag input. ---
assert_regex "${WORKFLOW}" '^  push:' "workflow triggers on push"
assert_regex "${WORKFLOW}" '^    tags:' "push trigger is tag-scoped"
assert_regex "${WORKFLOW}" '^      - "v\*"$' "push tags match v*"
assert_regex "${WORKFLOW}" '^  workflow_dispatch:' "manual dispatch is available"
assert_regex "${WORKFLOW}" '^      tag:' "manual dispatch requires a tag input"
assert_no_active "${WORKFLOW}" '^[[:space:]]+(branches|pull_request):' "workflow has no branch or pull-request trigger"

# --- Tag safety: format guard, existing tag checkout, and HEAD <=> tag check. ---
assert_fixed "${WORKFLOW}" 'release tag must look like' "dispatch/push tag format is guarded"
assert_fixed "${WORKFLOW}" 'refs/tags/${TAG}^{commit}' "tag is resolved to a commit"
assert_fixed "${WORKFLOW}" 'refs/tags/${{ steps.tag.outputs.tag }}' "checkout is pinned to the release tag"
assert_fixed "${WORKFLOW}" 'if [[ $head != "$tagged" ]]' "tag and checked-out commit must match"
assert_fixed "${WORKFLOW}" 'fetch-depth: 0' "full history and tags are fetched"

# --- Least privilege. ---
assert_regex "${WORKFLOW}" '^permissions:' "workflow declares permissions"
assert_regex "${WORKFLOW}" '^  contents: read$' "workflow default token is read-only"
assert_regex "${WORKFLOW}" '^    permissions:$' "release job scopes its own permissions"
assert_regex "${WORKFLOW}" '^      contents: write$' "release job can publish a release"
write_permissions="$(grep -c 'contents: write' "${WORKFLOW}" || true)"
if [ "${write_permissions}" -eq 1 ]; then
	pass "content write is granted to exactly one job"
else
	fail "content write must appear once, found ${write_permissions}"
fi
assert_no_active "${WORKFLOW}" 'permissions:[[:space:]]*write-all|contents:[[:space:]]*write-all' "workflow never grants write-all"

# --- Build inputs. ---
assert_fixed "${WORKFLOW}" 'CGO_ENABLED: "0"' "release builds disable cgo"
assert_fixed "${WORKFLOW}" 'actions/setup-go@v5' "Go is set up"
assert_fixed "${WORKFLOW}" 'go-version-file: go.mod' "Go version comes from go.mod"
assert_fixed "${WORKFLOW}" 'actions/setup-node@v4' "Node is set up"
assert_fixed "${WORKFLOW}" 'go test ./... -race' "race tests run before packaging"
assert_fixed "${WORKFLOW}" 'go vet ./...' "vet runs before packaging"
assert_fixed "${WORKFLOW}" 'npm ci' "UI dependencies are installed"
assert_fixed "${WORKFLOW}" 'npm run check' "UI type check runs"
assert_fixed "${WORKFLOW}" 'npm test' "UI unit tests run"
assert_fixed "${WORKFLOW}" 'npm run build' "UI production build runs"

# --- GoReleaser action, config, and token. ---
assert_fixed "${WORKFLOW}" 'goreleaser/goreleaser-action@v6' "GoReleaser action v6 is used"
assert_fixed "${WORKFLOW}" 'args: check' "GoReleaser config is validated"
assert_fixed "${WORKFLOW}" 'args: release --clean' "GoReleaser publishes with --clean"
assert_fixed "${WORKFLOW}" 'GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}' "release token is the workflow token"
assert_no_active "${WORKFLOW}" 'config:[[:space:]]' "GoReleaser uses the repo .goreleaser.yaml by default"

# --- Linux-only, no deferred agent/platform builds, no catalog publication. ---
assert_fixed "${WORKFLOW}" 'runs-on: ubuntu-' "release runner is Linux"
assert_no_active "${WORKFLOW}" 'devstack-agent|windows|darwin|GOOS=.*(windows|darwin)' "workflow has no agent or non-Linux build"
assert_no_active "${WORKFLOW}" 'php-catalog|catalogmerge|internal/catalog|phpcatalog' "workflow does not publish a PHP catalog"

# --- GoReleaser config contract. ---
assert_regex "${GORELEASER}" '^version: 2$' "GoReleaser config is v2"
assert_fixed "${GORELEASER}" 'project_name: devstack' "project name is devstack"
assert_regex "${GORELEASER}" '^[[:space:]]+- id: devstack$' "devstack build is defined"
assert_regex "${GORELEASER}" '^[[:space:]]+- id: devstack-helper$' "devstack-helper build is defined"
assert_no_active "${GORELEASER}" 'devstack-agent' "config has no agent build"
assert_no_active "${GORELEASER}" '^[[:space:]]+- (windows|darwin)$' "config targets no other OS"
assert_no_active "${GORELEASER}" '^[[:space:]]+- (arm64|386|arm)$' "config targets no other architecture"
assert_regex "${GORELEASER}" '^[[:space:]]+- linux$' "config targets Linux"
assert_regex "${GORELEASER}" '^[[:space:]]+- amd64$' "config targets amd64"
assert_fixed "${GORELEASER}" 'devstack_{{ .Version }}_linux_amd64' "archive name is the Linux release name"
assert_fixed "${GORELEASER}" 'checksums.txt' "checksums.txt is generated"
assert_regex "${GORELEASER}" '^[[:space:]]+- install.sh$' "install.sh is an archive member"
assert_fixed "${GORELEASER}" 'extra_files:' "release extra files are configured"
assert_fixed "${GORELEASER}" 'glob: install.sh' "install.sh is attached to the release"
assert_no_active "${GORELEASER}" 'php-catalog|catalogmerge|internal/catalog|phpcatalog' "config does not embed a PHP catalog"

# --- CI wiring keeps the release gates. ---
assert_fixed "${CI_WORKFLOW}" 'go test ./cmd/catalogmerge' "CI tests the catalog merge tool"
assert_fixed "${CI_WORKFLOW}" 'test/release/release_test.sh' "CI runs the release archive test"
assert_fixed "${CI_WORKFLOW}" 'test/release/installer_test.sh' "CI runs the installer test"
assert_fixed "${CI_WORKFLOW}" 'test/release/workflow_test.sh' "CI runs this workflow test"
assert_fixed "${CI_WORKFLOW}" 'shellcheck --severity=error' "CI lints shell scripts"
assert_fixed "${CI_WORKFLOW}" 'bash -n' "CI checks shell syntax"

if [ "${FAILURES}" -ne 0 ]; then
	printf 'workflow-test: %d assertion(s) failed\n' "${FAILURES}" >&2
	exit 1
fi
printf 'workflow-test: all assertions passed\n'
