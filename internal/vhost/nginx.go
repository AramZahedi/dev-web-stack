package vhost

import (
	"fmt"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func renderNginxSite(s SiteSpec, maxBody int64, tlsDir string) []byte {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("\n\n")

	names := siteServerNames(s)
	switch {
	case s.DesiredState == model.DesiredStopped:
		writeNginxStoppedServer(&b, s, names, false, tlsDir)
		b.WriteString("\n")
		writeNginxStoppedServer(&b, s, names, true, tlsDir)
	case s.TLS && s.ForceHTTPS:
		writeNginxRedirectServer(&b, s, names, false, "https", tlsDir)
		b.WriteString("\n")
		writeNginxServingServer(&b, s, names, maxBody, true, tlsDir)
	case s.TLS:
		writeNginxServingServer(&b, s, names, maxBody, false, tlsDir)
		b.WriteString("\n")
		writeNginxServingServer(&b, s, names, maxBody, true, tlsDir)
	default:
		writeNginxServingServer(&b, s, names, maxBody, false, tlsDir)
		b.WriteString("\n")
		writeNginxRedirectServer(&b, s, names, true, "http", tlsDir)
	}
	return []byte(b.String())
}

func writeNginxServerOpen(b *strings.Builder, listen, names string) {
	b.WriteString("server {\n")
	fmt.Fprintf(b, "    listen %s;\n", listen)
	fmt.Fprintf(b, "    server_name %s;\n", names)
}

func writeNginxSiteTLS(b *strings.Builder, s SiteSpec, tlsDir string) {
	certPath, keyPath := siteCertificatePaths(s, tlsDir)
	fmt.Fprintf(b, "    ssl_certificate     %s;\n", nginxValue(certPath))
	fmt.Fprintf(b, "    ssl_certificate_key %s;\n", nginxValue(keyPath))
	b.WriteString("    ssl_protocols TLSv1.2 TLSv1.3;\n")
}

func writeNginxDashboardTLS(b *strings.Builder, tlsDir string) {
	certPath, keyPath := dashboardCertificatePaths(tlsDir)
	fmt.Fprintf(b, "    ssl_certificate     %s;\n", nginxValue(certPath))
	fmt.Fprintf(b, "    ssl_certificate_key %s;\n", nginxValue(keyPath))
	b.WriteString("    ssl_protocols TLSv1.2 TLSv1.3;\n")
}

func writeNginxServingServer(b *strings.Builder, s SiteSpec, names string, maxBody int64, tls bool, tlsDir string) {
	listen := "80"
	if tls {
		listen = "443 ssl http2"
	}
	writeNginxServerOpen(b, listen, names)
	if tls {
		writeNginxSiteTLS(b, s, tlsDir)
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "    client_max_body_size %d;\n", maxBody)
	b.WriteString("\n")

	if s.Kind == model.SiteNode {
		writeNginxNodeBody(b, s)
	} else {
		writeNginxStaticBody(b, s)
	}
	b.WriteString("}\n")
}

func writeNginxStaticBody(b *strings.Builder, s SiteSpec) {
	fmt.Fprintf(b, "    root %s;\n", nginxValue(s.DocumentRoot))
	b.WriteString("    index index.html;\n")
	b.WriteString("    autoindex off;\n")
	b.WriteString("    add_header Cache-Control \"no-store\" always;\n")
	b.WriteString("\n")
	b.WriteString("    location / {\n")
	if s.SPAFallback {
		b.WriteString("        try_files $uri $uri/ /index.html;\n")
	} else {
		b.WriteString("        try_files $uri $uri/ =404;\n")
	}
	b.WriteString("    }\n")
	b.WriteString("\n")
	writeNginxDenyLocations(b)
}

func writeNginxNodeBody(b *strings.Builder, s SiteSpec) {
	b.WriteString("    error_page 502 503 504 =503 /_devstack/503.html;\n")
	b.WriteString("\n")
	writeNginxProxyLocation(b, s.BackendPort, true)
	b.WriteString("\n")
	b.WriteString("    location = /_devstack/503.html {\n")
	b.WriteString("        internal;\n")
	b.WriteString("        default_type text/html;\n")
	fmt.Fprintf(b, "        return 200 %s;\n", nginxValue(maintenanceHTML))
	b.WriteString("    }\n")
	b.WriteString("\n")
	writeNginxDenyLocations(b)
}

func writeNginxProxyLocation(b *strings.Builder, port int, interceptErrors bool) {
	b.WriteString("    location / {\n")
	fmt.Fprintf(b, "        proxy_pass http://127.0.0.1:%d;\n", port)
	b.WriteString("        proxy_http_version 1.1;\n")
	b.WriteString("        proxy_set_header Upgrade $http_upgrade;\n")
	b.WriteString("        proxy_set_header Connection \"upgrade\";\n")
	b.WriteString("        proxy_set_header Host $host;\n")
	b.WriteString("        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n")
	b.WriteString("        proxy_set_header X-Forwarded-Proto $scheme;\n")
	b.WriteString("        proxy_set_header X-Forwarded-Host $host;\n")
	b.WriteString("        proxy_read_timeout 120s;\n")
	if interceptErrors {
		b.WriteString("        proxy_intercept_errors on;\n")
	}
	b.WriteString("    }\n")
}

func writeNginxDenyLocations(b *strings.Builder) {
	b.WriteString("    location ~ /\\. {\n")
	b.WriteString("        deny all;\n")
	b.WriteString("    }\n")
	b.WriteString("\n")
	b.WriteString("    location ^~ /_devstack {\n")
	b.WriteString("        deny all;\n")
	b.WriteString("    }\n")
}

func writeNginxStoppedServer(b *strings.Builder, s SiteSpec, names string, tls bool, tlsDir string) {
	listen := "80"
	if tls {
		listen = "443 ssl http2"
	}
	writeNginxServerOpen(b, listen, names)
	if tls {
		writeNginxSiteTLS(b, s, tlsDir)
	}
	b.WriteString("    default_type text/html;\n")
	b.WriteString("\n")
	fmt.Fprintf(b, "    return 503 %s;\n", nginxValue(maintenanceHTML))
	b.WriteString("}\n")
}

func writeNginxRedirectServer(b *strings.Builder, s SiteSpec, names string, tls bool, scheme string, tlsDir string) {
	listen := "80"
	if tls {
		listen = "443 ssl http2"
	}
	writeNginxServerOpen(b, listen, names)
	if tls {
		writeNginxSiteTLS(b, s, tlsDir)
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "    return 308 %s://$host$request_uri;\n", scheme)
	b.WriteString("}\n")
}

func renderNginxDashboard(d DashboardSpec, maxBody int64, tlsDir string) []byte {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("\n\n")

	writeNginxServerOpen(&b, "443 ssl http2", d.Domain)
	writeNginxDashboardTLS(&b, tlsDir)
	b.WriteString("\n")
	fmt.Fprintf(&b, "    client_max_body_size %d;\n", maxBody)
	b.WriteString("\n")
	writeNginxProxyLocation(&b, d.UIPort, false)
	b.WriteString("}\n")
	return []byte(b.String())
}

func renderNginxDefault(tlsDir string) []byte {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("\n\n")

	for _, tls := range []bool{false, true} {
		if tls {
			writeNginxServerOpen(&b, "443 ssl http2 default_server", "_")
			writeNginxDashboardTLS(&b, tlsDir)
		} else {
			writeNginxServerOpen(&b, "80 default_server", "_")
		}
		b.WriteString("\n")
		b.WriteString("    error_page 404 =404 /_devstack/404.html;\n")
		b.WriteString("\n")
		b.WriteString("    location / {\n")
		b.WriteString("        return 404;\n")
		b.WriteString("    }\n")
		b.WriteString("\n")
		b.WriteString("    location = /_devstack/404.html {\n")
		b.WriteString("        internal;\n")
		b.WriteString("        default_type text/html;\n")
		fmt.Fprintf(&b, "        return 200 %s;\n", nginxValue(notFoundHTML))
		b.WriteString("    }\n")
		b.WriteString("}\n")
		if !tls {
			b.WriteString("\n")
		}
	}
	return []byte(b.String())
}

// nginxValue renders a token bare when it is unambiguous and a double-quoted,
// escaped string otherwise. Document roots containing '$' are rejected during
// validation because nginx cannot represent a literal dollar in a value.
func nginxValue(value string) string {
	if nginxBareSafe(value) {
		return value
	}
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

func nginxBareSafe(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("/._-:~+", rune(c)):
		default:
			return false
		}
	}
	return true
}
