# devstack Docker image

The image is Fedora 44 and runs the released devstack binaries without
systemd. The privileged helper, daemon, static/Node/PHP sites, and dashboard all
run inside the container.

The image uses **builtin serving only**. It does not include systemd, nginx, or
Apache, so the external `nginx`/`apache` serving modes are not available in this
container. The builtin Go proxy still serves every static, Node, and PHP site.

## Published ports

| Purpose | Host default | Container |
|---|---:|---:|
| HTTP sites | **8080** | 80 |
| HTTPS sites | **8443** | 443 |
| Dashboard / UI | **7070** | 7070 |

The ports bind to `127.0.0.1` by default, so the dashboard and sites are
available on the local machine but not exposed to the LAN. Set
`DEVSTACK_BIND_ADDRESS=0.0.0.0` if you deliberately want LAN access. The host
ports are explicit; they are not guessed. Use `docker compose` from the
repository root:

```sh
docker compose up -d --build
```

Or run the image directly:

```sh
docker build -t devstack:0.1.0-docker .
docker run -d --name devstack --restart unless-stopped \
  -p 127.0.0.1:8080:80 -p 127.0.0.1:8443:443 -p 127.0.0.1:7070:7070 \
  -v "$PWD/.docker-data/devstack:/home/devstack/.devstack" \
  -v "$PWD/.docker-data/helper:/var/lib/devstack-helper" \
  -v "$PWD/projects:/home/devstack/projects" \
  -v /etc/hosts:/host-hosts \
  devstack:0.1.0-docker
```

Open the dashboard at <http://localhost:7070>. Sites are exposed as
`http://<name>.test:8080` and `https://<name>.test:8443`.

The `/etc/hosts` mount lets the container's helper maintain the same
`# BEGIN devstack` block on the host. Without it, the dashboard still works, but
you must add a line such as `127.0.0.1 blog.test` to the host hosts file
yourself.

## Port customization

The host mapping is the normal Docker knob:

```sh
DEVSTACK_HOST_HTTP_PORT=18080 \
DEVSTACK_HOST_HTTPS_PORT=18443 \
DEVSTACK_HOST_UI_PORT=17070 \
docker compose up -d --build
```

The image builds its dashboard links with `8080` and `8443` as the public ports.
For a different public port mapping, rebuild with matching build arguments:

```sh
docker build \
  --build-arg PUBLIC_HTTP_PORT=18080 \
  --build-arg PUBLIC_HTTPS_PORT=18443 \
  -t devstack:custom .
```

and set `DEVSTACK_PUBLIC_HTTP_PORT=18080` /
`DEVSTACK_PUBLIC_HTTPS_PORT=18443` at runtime. The container's internal ports
(80, 443, 7070, and the loopback daemon ports) do not need to change.

## HTTPS trust

The container CA is exported at `.docker-data/helper/pki/root.crt`. To trust it on
Fedora for command-line tools:

```sh
sudo cp .docker-data/helper/pki/root.crt /etc/pki/ca-trust/source/anchors/devstack-container.crt
sudo update-ca-trust
```

Browsers may use a separate NSS/Firefox store; import the same certificate
there if you want a warning-free `https://<name>.test:8443`. A site can be
created with `--no-https` when only HTTP is needed.

## Data and projects

- `.docker-data/devstack`: daemon config, database, certificates, logs, installed
  runtimes.
- `.docker-data/helper`: CA and helper/vhost state.
- `projects`: put site files here and add sites with paths under
  `/home/devstack/projects` (also available as `/workspace`).

Runtimes are installed on demand from the dashboard or inside the container:

```sh
docker exec -u devstack -e HOME=/home/devstack devstack \
  devstack runtime install php@8.4.27-r1
```

## Operations

```sh
docker compose ps
docker compose logs -f devstack
docker compose down
```

`docker compose down` keeps `.docker-data` and `projects`. It does not remove
the `# BEGIN devstack` block from the host `/etc/hosts`; remove that block
manually if the container will not come back.

The helper must run as root inside the container because it owns the CA, hosts
file, and loopback forwarders. It does not expose a privileged port on the host
directly.
