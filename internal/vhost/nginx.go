package vhost

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func renderNginxSite(s SiteSpec, maxBody int64, tlsDir string, ports listenPorts) []byte {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("\n\n")

	names := siteServerNames(s)
	switch {
	case s.DesiredState == model.DesiredStopped:
		writeNginxStoppedServer(&b, s, names, false, tlsDir, ports)
		b.WriteString("\n")
		writeNginxStoppedServer(&b, s, names, true, tlsDir, ports)
	case s.TLS && s.ForceHTTPS:
		writeNginxRedirectServer(&b, s, names, false, "https", tlsDir, ports)
		b.WriteString("\n")
		writeNginxServingServer(&b, s, names, maxBody, true, tlsDir, ports)
	case s.TLS:
		writeNginxServingServer(&b, s, names, maxBody, false, tlsDir, ports)
		b.WriteString("\n")
		writeNginxServingServer(&b, s, names, maxBody, true, tlsDir, ports)
	default:
		writeNginxServingServer(&b, s, names, maxBody, false, tlsDir, ports)
		b.WriteString("\n")
		writeNginxRedirectServer(&b, s, names, true, "http", tlsDir, ports)
	}
	return []byte(b.String())
}

func nginxListenValue(port int, tls bool) string {
	if tls {
		return strconv.Itoa(port) + " ssl http2"
	}
	return strconv.Itoa(port)
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

func writeNginxServingServer(b *strings.Builder, s SiteSpec, names string, maxBody int64, tls bool, tlsDir string, ports listenPorts) {
	port := ports.http
	if tls {
		port = ports.https
	}
	writeNginxServerOpen(b, nginxListenValue(port, tls), names)
	if tls {
		writeNginxSiteTLS(b, s, tlsDir)
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "    client_max_body_size %d;\n", maxBody)
	b.WriteString("\n")

	if s.Kind == model.SiteNode {
		writeNginxNodeBody(b, s)
	} else if s.Kind == model.SitePHP {
		writeNginxPHPBody(b, s)
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

func writeNginxPHPBody(b *strings.Builder, s SiteSpec) {
	fmt.Fprintf(b, "    root %s;\n", nginxValue(s.DocumentRoot))
	b.WriteString("    index index.php index.html;\n")
	b.WriteString("    autoindex off;\n")
	b.WriteString("\n")
	// nginx uses the first matching regex location. Both dotfiles and PHP
	// source backups must reach their deny before the FastCGI location.
	writeNginxDotfileDeny(b)
	b.WriteString("\n")
	writeNginxSourceBackupDeny(b)
	b.WriteString("\n")
	writeNginxDevstackDeny(b)
	b.WriteString("\n")
	b.WriteString("    location / {\n")
	if s.FrontController {
		b.WriteString("        try_files $uri $uri/ @devstack_front_controller;\n")
	} else {
		b.WriteString("        try_files $uri $uri/ =404;\n")
	}
	b.WriteString("    }\n")
	b.WriteString("\n")
	writeNginxPHPLocation(b, s.BackendPort)
	if s.FrontController {
		b.WriteString("\n")
		writeNginxPHPFrontControllerLocation(b, s.BackendPort, s.DocumentRoot+"/index.php")
	}
}

func writeNginxPHPLocation(b *strings.Builder, port int) {
	b.WriteString("    location ~* ^(.+\\.php)(/.*)?$ {\n")
	b.WriteString("        fastcgi_split_path_info ^(.+\\.php)(/.*)$;\n")
	b.WriteString("        try_files $fastcgi_script_name =404;\n")
	fmt.Fprintf(b, "        fastcgi_pass 127.0.0.1:%d;\n", port)
	b.WriteString("        fastcgi_index index.php;\n")
	b.WriteString("        fastcgi_read_timeout 120s;\n")
	b.WriteString("        fastcgi_buffering off;\n")
	writeNginxFastCGIParams(b,
		"$document_root$fastcgi_script_name",
		"$fastcgi_script_name",
		"$fastcgi_path_info",
	)
	b.WriteString("    }\n")
}

func writeNginxPHPFrontControllerLocation(b *strings.Builder, port int, indexPath string) {
	b.WriteString("    location @devstack_front_controller {\n")
	fmt.Fprintf(b, "        fastcgi_pass 127.0.0.1:%d;\n", port)
	b.WriteString("        fastcgi_index index.php;\n")
	b.WriteString("        fastcgi_read_timeout 120s;\n")
	b.WriteString("        fastcgi_buffering off;\n")
	writeNginxFastCGIParams(b,
		nginxQuotedValue(indexPath),
		"/index.php",
		"$uri",
	)
	b.WriteString("    }\n")
}

// writeNginxFastCGIParams renders the explicit CGI environment. HTTP request
// headers are forwarded automatically by nginx; these values are the ones the
// FastCGI SAPI needs and that nginx does not synthesize by itself.
func writeNginxFastCGIParams(b *strings.Builder, scriptFilename, scriptName, pathInfo string) {
	fmt.Fprintf(b, "        fastcgi_param SCRIPT_FILENAME %s;\n", scriptFilename)
	fmt.Fprintf(b, "        fastcgi_param SCRIPT_NAME %s;\n", scriptName)
	fmt.Fprintf(b, "        fastcgi_param PATH_INFO %s;\n", pathInfo)
	b.WriteString("        fastcgi_param DOCUMENT_ROOT $document_root;\n")
	b.WriteString("        fastcgi_param QUERY_STRING $query_string;\n")
	b.WriteString("        fastcgi_param REQUEST_METHOD $request_method;\n")
	b.WriteString("        fastcgi_param REQUEST_URI $request_uri;\n")
	b.WriteString("        fastcgi_param SERVER_PROTOCOL $server_protocol;\n")
	b.WriteString("        fastcgi_param GATEWAY_INTERFACE CGI/1.1;\n")
	b.WriteString("        fastcgi_param HTTPS $https if_not_empty;\n")
	b.WriteString("        fastcgi_param SERVER_SOFTWARE nginx/$nginx_version;\n")
	b.WriteString("        fastcgi_param SERVER_NAME $server_name;\n")
	b.WriteString("        fastcgi_param SERVER_ADDR $server_addr;\n")
	b.WriteString("        fastcgi_param SERVER_PORT $server_port;\n")
	b.WriteString("        fastcgi_param REMOTE_ADDR $remote_addr;\n")
	b.WriteString("        fastcgi_param REMOTE_PORT $remote_port;\n")
	b.WriteString("        fastcgi_param CONTENT_TYPE $content_type;\n")
	b.WriteString("        fastcgi_param CONTENT_LENGTH $content_length;\n")
	b.WriteString("        fastcgi_param REDIRECT_STATUS 200;\n")
}

func writeNginxDenyLocations(b *strings.Builder) {
	writeNginxDotfileDeny(b)
	b.WriteString("\n")
	writeNginxDevstackDeny(b)
}

func writeNginxDotfileDeny(b *strings.Builder) {
	b.WriteString("    location ~ /\\. {\n")
	b.WriteString("        deny all;\n")
	b.WriteString("    }\n")
}

func writeNginxSourceBackupDeny(b *strings.Builder) {
	b.WriteString("    location ~* \\.php(?:~|\\.(?:bak|orig|save|old|swp|tmp))$ {\n")
	b.WriteString("        deny all;\n")
	b.WriteString("    }\n")
}

func writeNginxDevstackDeny(b *strings.Builder) {
	b.WriteString("    location ^~ /_devstack {\n")
	b.WriteString("        deny all;\n")
	b.WriteString("    }\n")
}

func writeNginxStoppedServer(b *strings.Builder, s SiteSpec, names string, tls bool, tlsDir string, ports listenPorts) {
	port := ports.http
	if tls {
		port = ports.https
	}
	writeNginxServerOpen(b, nginxListenValue(port, tls), names)
	if tls {
		writeNginxSiteTLS(b, s, tlsDir)
	}
	b.WriteString("    default_type text/html;\n")
	b.WriteString("\n")
	fmt.Fprintf(b, "    return 503 %s;\n", nginxValue(maintenanceHTML))
	b.WriteString("}\n")
}

func writeNginxRedirectServer(b *strings.Builder, s SiteSpec, names string, tls bool, scheme string, tlsDir string, ports listenPorts) {
	port := ports.http
	if tls {
		port = ports.https
	}
	writeNginxServerOpen(b, nginxListenValue(port, tls), names)
	if tls {
		writeNginxSiteTLS(b, s, tlsDir)
	}
	b.WriteString("\n")

	targetPort := ports.http
	if scheme == "https" {
		targetPort = ports.https
	}
	authority := "$host"
	if targetPort != schemeDefaultPort(scheme) {
		authority = fmt.Sprintf("$host:%d", targetPort)
	}
	fmt.Fprintf(b, "    return 308 %s://%s$request_uri;\n", scheme, authority)
	b.WriteString("}\n")
}

func renderNginxDashboard(d DashboardSpec, maxBody int64, tlsDir string, ports listenPorts) []byte {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("\n\n")

	writeNginxServerOpen(&b, nginxListenValue(ports.https, true), d.Domain)
	writeNginxDashboardTLS(&b, tlsDir)
	b.WriteString("\n")
	fmt.Fprintf(&b, "    client_max_body_size %d;\n", maxBody)
	b.WriteString("\n")
	writeNginxProxyLocation(&b, d.UIPort, false)
	b.WriteString("}\n")
	return []byte(b.String())
}

func renderNginxDefault(tlsDir string, ports listenPorts, suppressDefaultServer bool) []byte {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("\n\n")

	marker := " default_server"
	if suppressDefaultServer {
		marker = ""
	}
	for _, tls := range []bool{false, true} {
		if tls {
			writeNginxServerOpen(&b, nginxListenValue(ports.https, true)+marker, "_")
			writeNginxDashboardTLS(&b, tlsDir)
		} else {
			writeNginxServerOpen(&b, nginxListenValue(ports.http, false)+marker, "_")
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
	return nginxQuotedValue(value)
}

// nginxQuotedValue always emits a double-quoted nginx string. It is used where
// the quoting itself must be visible, such as a fixed SCRIPT_FILENAME.
func nginxQuotedValue(value string) string {
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
