# Linux host-tuning E2E

This root-only probe applies a computed performance profile to the real Linux
host tuning paths, verifies the generated files and live `/proc/sys` values,
then restores the pre-devstack state.

```sh
go build -o /tmp/devstack-linux-performance ./test/e2e/linux-performance
sudo /tmp/devstack-linux-performance -profile throughput
```

The default backup root is a temporary directory. Pass `-backup-root` to keep
the performance backup under a specific devstack root. The probe refuses to run
without root because the helper normally writes `/etc/sysctl.d` and
`/etc/security/limits.d`.

This is a supervised acceptance probe, not part of `go test ./...`; it changes
machine-wide kernel settings and must be run on a disposable or trusted host.
