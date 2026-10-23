# TLS and trust

devstack runs a local certificate authority. The root CA belongs to the
privileged helper; the daemon asks the helper to sign a short-lived certificate
for each site, then serves it through the builtin HTTPS proxy. Browsers and Node
accept the sites only after the root CA is trusted.

## How it fits together

| Piece | Location | Owner |
|---|---|---|
| Root CA and private key | `/var/lib/devstack-helper/pki` | root (helper) |
| Daemon CA bundle | `~/.devstack/pki/ca-bundle.crt` | your user |
| Site certificates | `~/.devstack/pki/<site-id>.crt` and `.key` | your user |
| Managed hosts block | `/etc/hosts` | root (helper) |
| OS anchor | distribution CA anchor directory | root (helper) |
| Chrome/Firefox trust | NSS databases under your home | helper, run as you |

`devstack init` asks the helper to create the CA if needed, installs the root in
the operating-system trust store and in Chrome/Firefox NSS databases, and writes
the hosts block. It then starts the daemon, which refreshes the combined
`ca-bundle.crt` used by Node and by command-line tools you point at it.

The root private key never leaves the helper's root-only state directory. Site
keys are written by the daemon with owner-only permissions.

## Check trust

```sh
devstack trust status
devstack trust status --json
```

The table has one row per target:

- `OS` — the distribution's CA anchor.
- `Chrome` — the shared Chrome NSS database at `~/.pki/nssdb`.
- `Firefox (<profile>)` — each Firefox profile under your home.

`installed` means the helper's current root fingerprint is present. `not
installed` means the target exists but lacks the CA, and `not installed` in the
detail column for Chrome/Firefox can also mean the browser (or `certutil`) is not
available.

## Install or repair trust

```sh
devstack trust repair
```

`repair` is idempotent: it reinstalls the CA in every target found for your user.
Run it after installing `certutil`, after changing your Firefox profiles, or
after a browser reports an untrusted certificate.

The full diagnostic and repair path is:

```sh
devstack doctor
devstack doctor --repair
```

`doctor --repair` re-applies trust, rebuilds the hosts block, and verifies
installed runtimes. It does not remove data or project files.

If `certutil` is missing, install NSS tools and repair again:

```sh
# Fedora / RHEL
sudo dnf install nss-tools

# Debian / Ubuntu
sudo apt install libnss3-tools

devstack trust repair
```

## Remove trust

```sh
devstack trust remove --yes
```

This removes only the CA whose fingerprint matches the current helper root. It
does not touch unrelated certificates. `devstack uninstall --yes` also removes
trust as part of uninstalling.

## Use the CA from the command line

The daemon bundle contains the system roots plus the devstack root:

```sh
curl --cacert "$HOME/.devstack/pki/ca-bundle.crt" https://blog.test
```

No `--resolve` or `-k` is needed when `/etc/hosts` contains the managed block.
For a site that is not in the hosts file yet, point curl at loopback explicitly:

```sh
curl --cacert "$HOME/.devstack/pki/ca-bundle.crt" \
  --resolve blog.test:443:127.0.0.1 \
  https://blog.test/
```

Node sites receive `NODE_EXTRA_CA_CERTS` automatically:

```js
// server-side fetch to another devstack site
const response = await fetch("https://blog.test");
```

For a script outside a managed Node site, set it yourself:

```sh
NODE_EXTRA_CA_CERTS="$HOME/.devstack/pki/ca-bundle.crt" node script.js
```

The bundle is mode `0600`; tools running as another user cannot read it. That is
intentional.

## Troubleshooting

**Browser still warns after `trust repair`.** Restart the browser so it reloads
its NSS database. Confirm `devstack trust status` shows the target as installed.
If the browser is a sandboxed build (Snap, Flatpak, or a profile outside your
home), the helper intentionally refuses paths outside your home; import
`~/.devstack/pki/ca-bundle.crt` through that browser's own settings instead.

**`OS` target is not installed or unsupported.** Check that the helper is
running and that the distribution has a known CA anchor directory:

```sh
sudo systemctl status devstack-helper --no-pager
sudo journalctl -u devstack-helper -n 100 --no-pager
```

Fedora/RHEL use `/etc/pki/ca-trust/source/anchors` and `update-ca-trust`;
Debian/Ubuntu use `/usr/local/share/ca-certificates` and
`update-ca-certificates`. To override detection, set `trust_dir` in
`/etc/devstack/helper.yaml` and restart the helper.

**Trust repair says the helper is unavailable.** The helper must be running and
its `allow_uid` must equal your `id -u`. See [Install](install.md).

**Certificate warnings on a site you just added.** `site add` issues the
certificate before starting the site. Check `devstack site show <name>` and
`devstack site logs <name>`, then run `devstack doctor`. A daemon restart
reloads the certificate and route tables.

**Hosts changes do not resolve.** `devstack init` or `devstack doctor --repair`
rebuilds the block between `# BEGIN devstack` and `# END devstack` in
`/etc/hosts`. A malformed block must be fixed manually before repair can run.

## Security notes

- The root CA is local to this machine. Treat it like any development CA: do
  not copy the private key, and remove it with `devstack trust remove` when it is
  no longer needed.
- Trust changes require the privileged helper; the unprivileged daemon cannot
  write system trust stores or `/etc/hosts`.
- The helper only modifies trust for its configured `allow_uid` and only paths
  inside that user's home.
- Plan 1 uses the local CA for the dashboard and every site. Public certificates,
  per-site ACME, and Windows/WSL trust are later milestones.
