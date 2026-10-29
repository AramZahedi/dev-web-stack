# Comprehensive Services Deep E2E & Configuration Testing Plan

> **Goal**: Deeply test all 7 infrastructure services (nginx, apache, mysql, mariadb, postgresql, redis, phpmyadmin) on Windows, including mutual exclusivity, installation, live container activation, real network connectivity (TCP/HTTP), live log inspection, configuration lifecycle (list, show, diff, apply, restart, verify, reset), database credentials & rotation, UI dashboard integration, and clean teardown.

## Phase 1: Web Servers Group (nginx & apache)
- [ ] Task 1.1: **nginx**:
  - Install `nginx` (`devstack service install nginx`).
  - Scaffold verification: inspect seeded `nginx.conf` and virtual host configuration.
  - Activate `nginx` (`devstack service activate nginx`).
  - Verify container status in `docker ps` and live HTTP response on port 18080 (`curl.exe http://127.0.0.1:18080`).
  - Inspect live logs (`devstack service logs nginx`).
  - Configuration testing:
    - List config files (`devstack service config list nginx`).
    - View `nginx.conf` (`devstack service config show nginx nginx.conf`).
    - Test diff (`devstack service config diff nginx nginx.conf ...`).
    - Apply custom directive/header and verify backup creation.
    - Restart `nginx` and verify reload with new configuration.
    - Reset configuration (`devstack service config reset nginx nginx.conf`) and verify rollback.
- [ ] Task 1.2: **Mutual Exclusivity (Web Group)**:
  - While `nginx` is active, attempt to activate `apache`. Verify daemon rejects activation with mutual exclusivity conflict error.
  - Deactivate `nginx` (`devstack service deactivate nginx`).
- [ ] Task 1.3: **apache**:
  - Install `apache` (`devstack service install apache`).
  - Scaffold verification: inspect seeded `httpd.conf`.
  - Activate `apache` (`devstack service activate apache`).
  - Verify container status and live HTTP response on port 18080 (`curl.exe http://127.0.0.1:18080`).
  - Inspect live logs (`devstack service logs apache`).
  - Configuration testing: list, show, diff, apply edit (e.g. ServerTokens), restart, reset.
  - Deactivate and uninstall `apache` and `nginx`.

## Phase 2: MySQL Family Group (mysql & mariadb)
- [ ] Task 2.1: **mysql (MySQL 8.4)**:
  - Install `mysql` (`devstack service install mysql`).
  - Inspect seeded `my.cnf`.
  - Activate `mysql` (`devstack service activate mysql`).
  - Wait for database initialization and verify container health in `docker ps`.
  - Test real TCP network connectivity on port 3306 (MySQL handshake/greeting).
  - Inspect live logs (`devstack service logs mysql`).
  - Test credentials inspection (`devstack service credentials mysql`).
  - Test credentials rotation (`devstack service rotate-credentials mysql` or equivalent API).
  - Configuration testing:
    - List config files (`devstack service config list mysql`).
    - Show `my.cnf`.
    - Apply custom parameter (e.g. `max_connections = 250`).
    - Restart `mysql` and verify container accepts connections with modified config.
    - Reset `my.cnf` and verify rollback.
- [ ] Task 2.2: **Mutual Exclusivity (MySQL Family Group)**:
  - While `mysql` is active, attempt to activate `mariadb`. Verify rejection with exclusivity conflict.
  - Deactivate `mysql`.
- [ ] Task 2.3: **mariadb (MariaDB 11.8)**:
  - Install `mariadb` (`devstack service install mariadb`).
  - Inspect seeded `my.cnf`.
  - Activate `mariadb` (`devstack service activate mariadb`).
  - Verify container health and real TCP handshake on port 3306.
  - Inspect live logs (`devstack service logs mariadb`).
  - Test credentials inspection and rotation.
  - Configuration testing: list, show, diff, apply parameter edit, restart, reset.
  - Deactivate and uninstall `mariadb` and `mysql`.

## Phase 3: Standalone Databases & Caches (postgresql & redis)
- [ ] Task 3.1: **postgresql (PostgreSQL 18)**:
  - Install `postgresql` (`devstack service install postgresql`).
  - Inspect seeded `postgresql.conf` and `pg_hba.conf`.
  - Activate `postgresql` (`devstack service activate postgresql`).
  - Verify container health and test real TCP handshake on port 5432.
  - Inspect live logs (`devstack service logs postgresql`).
  - Test credentials inspection and rotation.
  - Configuration testing:
    - List config files (`devstack service config list postgresql`).
    - Show `postgresql.conf`.
    - Apply custom parameter (e.g. `statement_timeout = 30000`).
    - Restart `postgresql` and verify container health.
    - Reset `postgresql.conf` and verify rollback.
- [ ] Task 3.2: **redis (Redis 8.8)**:
  - Install `redis` (`devstack service install redis`).
  - Inspect seeded `redis.conf`.
  - Activate `redis` (`devstack service activate redis`).
  - Test TCP ping `PING\r\n` -> `+PONG` on port 6379.
  - Inspect live logs (`devstack service logs redis`).
  - Configuration testing: apply custom `timeout 60` in `redis.conf`, restart, verify, reset.
  - Deactivate both.

## Phase 4: Web Management & Concurrency (phpmyadmin & multi-service)
- [ ] Task 4.1: **Concurrent Stack Activation**:
  - Activate `mysql` + `redis` + `phpmyadmin` simultaneously.
  - Verify all 3 containers running concurrently in WSL (`docker ps`).
  - Test `phpmyadmin` HTTP response on port 8082 (`curl.exe http://127.0.0.1:8082`).
  - Inspect `phpmyadmin` logs.
  - Verify `phpmyadmin` connects to the running `mysql` instance.

## Phase 5: Dashboard UI Services Audit (Playwright)
- [ ] Task 5.1: Navigate to `http://127.0.0.1:7070/#/services` while services are active.
- [ ] Task 5.2: Verify service cards show active status, container IDs, and port mappings.
- [ ] Task 5.3: Open logs drawer for a service and verify live log streaming in UI.
- [ ] Task 5.4: Open configuration editor drawer, test editing and diff preview in UI.
- [ ] Task 5.5: Open credentials drawer, test password reveal.

## Phase 6: Clean Teardown & Final Acceptance
- [ ] Task 6.1: Deactivate and uninstall all services with `--delete-data --yes`.
- [ ] Task 6.2: Confirm WSL `docker ps` and volume/network cleanup.
- [ ] Task 6.3: Verify dashboard and CLI show all services back to `not_installed`.
- [ ] Task 6.4: Run full regression test suites (`go test ./...` and `npm --prefix ui test`).
- [ ] Task 6.5: Final sign-off.
