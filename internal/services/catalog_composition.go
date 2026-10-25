package services

import "time"

// phpmyadminImage is the pinned phpMyAdmin 5.2.3 image. The tag is kept
// alongside the digest; the digest is authoritative.
const phpmyadminImage = "phpmyadmin:5.2.3@sha256:62a50b1419fe55b9b4c3cd6f5aed03a18f8f5b4a236f24735d76400bea75a5ff"

// standaloneServices returns the standalone catalog entries.
func standaloneServices() []Service {
	return []Service{
		{
			ID:          IDPHPMyAdmin,
			DisplayName: "phpMyAdmin 5.2",
			Group:       GroupStandalone,
			Image:       phpmyadminImage,
			NetworkMode: "bridge",
			Ports: []Port{
				{Host: 8082, Container: 80, Protocol: "http", Loopback: true},
			},
			Config: []Mount{
				{
					HostSubdir:    "config/config.user.inc.php",
					ContainerPath: "/etc/phpmyadmin/config.user.inc.php",
					ReadOnly:      true,
					Area:          MountService,
					SELinux:       true,
				},
			},
			Environment: map[string]string{
				"PMA_HOST":         "host.docker.internal",
				"PMA_PORT":         "3306",
				"PMA_ABSOLUTE_URI": "http://127.0.0.1:8082/",
				"PMA_ARBITRARY":    "0",
			},
			// Keep the host-gateway mapping used by Docker Desktop. Native
			// Linux cannot reach a loopback-published database port through
			// the bridge gateway, so config.user.inc.php points phpMyAdmin at
			// the fixed "db" network alias carried by the active MySQL family.
			ExtraHosts: []string{"host.docker.internal:host-gateway"},
			Healthcheck: &Healthcheck{
				Test:        []string{"CMD", "curl", "-fsS", "-o", "/dev/null", "http://127.0.0.1/"},
				Interval:    10 * time.Second,
				Timeout:     5 * time.Second,
				StartPeriod: 30 * time.Second,
				Retries:     5,
			},
			SecurityOpts: []string{noNewPrivileges},
		},
	}
}
