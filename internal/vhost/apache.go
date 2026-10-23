package vhost

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func renderApacheSite(s SiteSpec, maxBody int64, tlsDir string, ports listenPorts) []byte {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("\n\n")

	switch {
	case s.DesiredState == model.DesiredStopped:
		writeApacheStoppedVHost(&b, s, false, tlsDir, ports)
		b.WriteString("\n")
		writeApacheStoppedVHost(&b, s, true, tlsDir, ports)
	case s.TLS && s.ForceHTTPS:
		writeApacheRedirectVHost(&b, s, false, "https", tlsDir, ports)
		b.WriteString("\n")
		writeApacheServingVHost(&b, s, maxBody, true, tlsDir, ports)
	case s.TLS:
		writeApacheServingVHost(&b, s, maxBody, false, tlsDir, ports)
		b.WriteString("\n")
		writeApacheServingVHost(&b, s, maxBody, true, tlsDir, ports)
	default:
		writeApacheServingVHost(&b, s, maxBody, false, tlsDir, ports)
		b.WriteString("\n")
		writeApacheRedirectVHost(&b, s, true, "http", tlsDir, ports)
	}
	return []byte(b.String())
}

func apacheVHostPort(ports listenPorts, tls bool) int {
	if tls {
		return ports.https
	}
	return ports.http
}

func writeApacheVHostOpen(b *strings.Builder, port int, s SiteSpec) {
	fmt.Fprintf(b, "<VirtualHost *:%d>\n", port)
	fmt.Fprintf(b, "    ServerName %s\n", s.Domain)
	if s.WWW {
		fmt.Fprintf(b, "    ServerAlias www.%s\n", s.Domain)
	}
}

func writeApacheTLSDirectives(b *strings.Builder, certPath, keyPath string) {
	b.WriteString("    SSLEngine on\n")
	fmt.Fprintf(b, "    SSLCertificateFile %s\n", apacheQuote(certPath))
	fmt.Fprintf(b, "    SSLCertificateKeyFile %s\n", apacheQuote(keyPath))
	b.WriteString("    SSLProtocol -all +TLSv1.2 +TLSv1.3\n")
}

func writeApacheSiteTLS(b *strings.Builder, s SiteSpec, tlsDir string) {
	certPath, keyPath := siteCertificatePaths(s, tlsDir)
	writeApacheTLSDirectives(b, certPath, keyPath)
}

func writeApacheServingVHost(b *strings.Builder, s SiteSpec, maxBody int64, tls bool, tlsDir string, ports listenPorts) {
	writeApacheVHostOpen(b, apacheVHostPort(ports, tls), s)
	if tls {
		writeApacheSiteTLS(b, s, tlsDir)
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "    LimitRequestBody %d\n", maxBody)
	b.WriteString("\n")

	if s.Kind == model.SiteNode {
		scheme := "http"
		if tls {
			scheme = "https"
		}
		writeApacheProxyConfig(b, s.BackendPort, scheme)
		b.WriteString("\n")
		fmt.Fprintf(b, "    ErrorDocument 503 %s\n", apacheQuote(maintenanceHTML))
		b.WriteString("\n")
		writeApacheDenyLocation(b)
	} else {
		writeApacheStaticBody(b, s)
	}
	b.WriteString("</VirtualHost>\n")
}

func writeApacheStaticBody(b *strings.Builder, s SiteSpec) {
	fmt.Fprintf(b, "    DocumentRoot %s\n", apacheQuote(s.DocumentRoot))
	fmt.Fprintf(b, "    <Directory %s>\n", apacheQuote(s.DocumentRoot))
	b.WriteString("        Options -Indexes\n")
	b.WriteString("        AllowOverride None\n")
	b.WriteString("        Require all granted\n")
	if s.SPAFallback {
		b.WriteString("        FallbackResource /index.html\n")
	}
	b.WriteString("    </Directory>\n")
	b.WriteString("\n")
	b.WriteString("    <FilesMatch \"^\\.\">\n")
	b.WriteString("        Require all denied\n")
	b.WriteString("    </FilesMatch>\n")
	b.WriteString("\n")
	writeApacheDenyLocation(b)
	b.WriteString("\n")
	b.WriteString("    Header always set Cache-Control \"no-store\"\n")
}

func writeApacheDenyLocation(b *strings.Builder) {
	b.WriteString("    <LocationMatch \"^/_devstack(/|$)\">\n")
	b.WriteString("        Require all denied\n")
	b.WriteString("    </LocationMatch>\n")
}

func writeApacheProxyConfig(b *strings.Builder, port int, scheme string) {
	upstream := "http://127.0.0.1:" + strconv.Itoa(port) + "/"
	quotedUpstream := apacheQuote(upstream)

	b.WriteString("    ProxyPreserveHost On\n")
	b.WriteString("    ProxyAddHeaders Off\n")
	fmt.Fprintf(b, "    ProxyPass \"/\" %s upgrade=websocket connectiontimeout=5 timeout=120\n", quotedUpstream)
	fmt.Fprintf(b, "    ProxyPassReverse \"/\" %s\n", quotedUpstream)
	b.WriteString("\n")
	b.WriteString("    RewriteEngine On\n")
	b.WriteString("    RewriteCond %{HTTP_HOST} ^(.+)$\n")
	b.WriteString("    RewriteRule ^ - [E=DEVSTACK_HOST:%1]\n")
	b.WriteString("    RewriteCond %{REMOTE_ADDR} ^(.+)$\n")
	b.WriteString("    RewriteRule ^ - [E=DEVSTACK_REMOTE:%1]\n")
	b.WriteString("\n")
	b.WriteString("    RequestHeader set Host \"%{DEVSTACK_HOST}e\"\n")
	b.WriteString("    RequestHeader set X-Forwarded-For \"%{DEVSTACK_REMOTE}e\"\n")
	fmt.Fprintf(b, "    RequestHeader set X-Forwarded-Proto %s\n", apacheQuote(scheme))
	b.WriteString("    RequestHeader set X-Forwarded-Host \"%{DEVSTACK_HOST}e\"\n")
}

func writeApacheStoppedVHost(b *strings.Builder, s SiteSpec, tls bool, tlsDir string, ports listenPorts) {
	writeApacheVHostOpen(b, apacheVHostPort(ports, tls), s)
	if tls {
		writeApacheSiteTLS(b, s, tlsDir)
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "    ErrorDocument 503 %s\n", apacheQuote(maintenanceHTML))
	b.WriteString("    RewriteEngine On\n")
	b.WriteString("    RewriteRule ^ - [R=503,L]\n")
	b.WriteString("</VirtualHost>\n")
}

func writeApacheRedirectVHost(b *strings.Builder, s SiteSpec, tls bool, scheme string, tlsDir string, ports listenPorts) {
	writeApacheVHostOpen(b, apacheVHostPort(ports, tls), s)
	if tls {
		writeApacheSiteTLS(b, s, tlsDir)
	}
	b.WriteString("\n")
	b.WriteString("    RewriteEngine On\n")
	fmt.Fprintf(b, "    RewriteRule ^ %s://%%{HTTP_HOST}%%{REQUEST_URI} [R=308,L]\n", scheme)
	b.WriteString("</VirtualHost>\n")
}

func renderApacheDashboard(d DashboardSpec, maxBody int64, tlsDir string, ports listenPorts) []byte {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "<VirtualHost *:%d>\n", ports.https)
	fmt.Fprintf(&b, "    ServerName %s\n", d.Domain)
	certPath, keyPath := dashboardCertificatePaths(tlsDir)
	writeApacheTLSDirectives(&b, certPath, keyPath)
	b.WriteString("\n")
	fmt.Fprintf(&b, "    LimitRequestBody %d\n", maxBody)
	b.WriteString("\n")
	writeApacheProxyConfig(&b, d.UIPort, "https")
	b.WriteString("</VirtualHost>\n")
	return []byte(b.String())
}

func renderApacheDefault(tlsDir string, ports listenPorts) []byte {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("\n\n")
	b.WriteString("# This file sorts first; Apache uses the first vhost for each port as the default.\n")

	for _, tls := range []bool{false, true} {
		port := apacheVHostPort(ports, tls)
		fmt.Fprintf(&b, "<VirtualHost *:%d>\n", port)
		b.WriteString("    ServerName devstack.invalid\n")
		if tls {
			certPath, keyPath := dashboardCertificatePaths(tlsDir)
			writeApacheTLSDirectives(&b, certPath, keyPath)
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "    ErrorDocument 404 %s\n", apacheQuote(notFoundHTML))
		b.WriteString("    RewriteEngine On\n")
		b.WriteString("    RewriteRule ^ - [R=404,L]\n")
		b.WriteString("</VirtualHost>\n")
		if !tls {
			b.WriteString("\n")
		}
	}
	return []byte(b.String())
}

// apacheQuote renders one Apache configuration argument. Paths are always
// quoted, with backslashes and double quotes escaped. Callers validate values
// first, so control characters never reach this function.
func apacheQuote(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\', '"':
			b.WriteByte('\\')
		}
		b.WriteByte(value[i])
	}
	b.WriteByte('"')
	return b.String()
}
