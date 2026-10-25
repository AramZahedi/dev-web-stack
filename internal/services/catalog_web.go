package services

import "time"

// webServices returns the Docker-hosted web-server catalog entries. Both run
// with host networking because they own 80/443 and reach native PHP-FPM/Node
// listeners on loopback. Ports remain in the catalog for status and activation
// checks even though Compose does not publish them for host-network services.
func webServices() []Service {
	return []Service{
		{
			ID:          IDNginx,
			DisplayName: "nginx",
			Group:       GroupWeb,
			Image:       "nginx:1.30.6@sha256:a7675fcee220f59b4a99f7098c06a0d5d999c2c0ee37c0211efb780536e5cb20",
			NetworkMode: "host",
			Ports: []Port{
				{Host: 80, Container: 80, Protocol: "http"},
				{Host: 443, Container: 443, Protocol: "https"},
			},
			Config: []Mount{
				{HostSubdir: "config/nginx.conf", ContainerPath: "/etc/nginx/nginx.conf", ReadOnly: true, Area: MountService},
				{HostSubdir: "config/conf.d", ContainerPath: "/etc/nginx/conf.d", ReadOnly: true, Area: MountService},
				{HostSubdir: "logs", ContainerPath: "/var/log/nginx", Area: MountService},
			},
			Data: []Mount{
				{HostSubdir: "", ContainerPath: "/sites", ReadOnly: true, Area: MountProjects},
				{HostSubdir: "", ContainerPath: "/certs", ReadOnly: true, Area: MountCerts},
			},
			Healthcheck: &Healthcheck{
				Test:        []string{"CMD-SHELL", "nginx -t"},
				Interval:    30 * time.Second,
				Timeout:     5 * time.Second,
				StartPeriod: 5 * time.Second,
				Retries:     3,
			},
			ValidateCommand: []string{"nginx", "-t"},
			ReloadCommand:   []string{"nginx", "-s", "reload"},
			SecurityOpts:    []string{"no-new-privileges:true"},
		},
		{
			ID:          IDApache,
			DisplayName: "Apache HTTP Server",
			Group:       GroupWeb,
			Image:       "httpd:2.4.69@sha256:a30cd4cd57f3f3c3be9b2d16384d2488839996bb14c67df2b51f1ecbb3e584a1",
			NetworkMode: "host",
			Ports: []Port{
				{Host: 80, Container: 80, Protocol: "http"},
				{Host: 443, Container: 443, Protocol: "https"},
			},
			Config: []Mount{
				{HostSubdir: "config/httpd.conf", ContainerPath: "/usr/local/apache2/conf/httpd.conf", ReadOnly: true, Area: MountService},
				{HostSubdir: "config/conf.d", ContainerPath: "/usr/local/apache2/conf/conf.d", ReadOnly: true, Area: MountService},
				{HostSubdir: "logs", ContainerPath: "/usr/local/apache2/logs", Area: MountService},
			},
			Data: []Mount{
				{HostSubdir: "", ContainerPath: "/sites", ReadOnly: true, Area: MountProjects},
				{HostSubdir: "", ContainerPath: "/certs", ReadOnly: true, Area: MountCerts},
			},
			Healthcheck: &Healthcheck{
				Test:        []string{"CMD-SHELL", "apachectl configtest"},
				Interval:    30 * time.Second,
				Timeout:     5 * time.Second,
				StartPeriod: 5 * time.Second,
				Retries:     3,
			},
			ValidateCommand: []string{"apachectl", "configtest"},
			ReloadCommand:   []string{"apachectl", "graceful"},
			SecurityOpts:    []string{"no-new-privileges:true"},
		},
	}
}
