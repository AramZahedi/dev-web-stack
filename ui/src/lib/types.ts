export type SiteKind = 'static' | 'php' | 'node';
export type BackendKind = 'local' | 'wsl';
export type DesiredState = 'running' | 'stopped';
export type SiteStatus =
  | 'stopped'
  | 'starting'
  | 'running'
  | 'stopping'
  | 'crashed'
  | 'error';

export type CheckStatus = 'ok' | 'warn' | 'fail' | 'skipped';

export interface CertificateInfo {
  not_after: string;
  domains: string[];
  serial?: string;
}

export interface Site {
  id: string;
  name: string;
  domain: string;
  www: boolean;
  backend: BackendKind;
  host_path: string;
  backend_path: string;
  kind: SiteKind;
  php_version: string;
  php_extensions?: Record<string, boolean>;
  php_ini?: Record<string, string>;
  node_version: string;
  command: string;
  env: Record<string, string>;
  document_root: string;
  front_controller: boolean;
  spa_fallback: boolean;
  tls: boolean;
  force_https: boolean;
  desired_state: DesiredState;
  status: SiteStatus;
  http_port: number;
  fastcgi_port: number;
  created_at: string;
  updated_at: string;
  /**
   * Optional certificate metadata. The M1 API exposes the site without it;
   * when absent the dashboard describes the TLS state but not an expiry.
   */
  certificate?: CertificateInfo;
}

export interface CreateSiteRequest {
  name: string;
  path: string;
  kind: SiteKind;
  node_version?: string;
  command?: string;
  document_root?: string;
  www?: boolean;
  tls?: boolean;
  force_https?: boolean;
  spa_fallback?: boolean;
  env?: Record<string, string>;
}

export interface UpdateSiteRequest {
  name?: string;
  domain?: string;
  www?: boolean;
  node_version?: string;
  command?: string;
  document_root?: string;
  spa_fallback?: boolean;
  tls?: boolean;
  force_https?: boolean;
  env?: Record<string, string>;
}

export interface RuntimeBuild {
  id: string;
  kind: string;
  version: string;
  backend: BackendKind;
  path: string;
  checksum: string;
  installed_at: string;
  /** Optional: the M1 API does not report the extracted size. */
  size?: number;
}

export interface CatalogEntry {
  id: string;
  kind: string;
  version: string;
  build: string;
  url: string;
  sha256: string;
  size: number;
}

export interface DoctorCheck {
  id: string;
  title: string;
  status: CheckStatus;
  detail: string;
  remediation: string;
}

export interface DoctorReport {
  checks: DoctorCheck[];
  ok: boolean;
  last_run?: string;
}

export interface TrustTarget {
  name: string;
  ok: boolean;
  detail: string;
}

export interface Settings {
  home: string;
  http_port: number;
  https_port: number;
  ui_port: number;
  wsl_distro: string;
  auto_start_sites: boolean;
  graceful_stop_seconds: number;
  max_request_body_mb: number;
  log_max_mb: number;
  log_keep: number;
  version?: string;
  socket_path?: string;
  pid_path?: string;
  /** Optional convenience field; real daemons may omit it. */
  user_home?: string;
}

export interface FsEntry {
  name: string;
  path: string;
}

export interface FsListing {
  path: string;
  entries: FsEntry[];
}

export interface Health {
  status: string;
  version: string;
  /** Optional fields returned by the mock; real daemons may omit them. */
  helper?: 'ok' | 'warn' | 'down' | 'unknown';
  helper_version?: string;
  ca_fingerprint?: string;
}

export interface LogLine {
  time: string;
  source: 'app' | 'fpm' | 'proxy' | 'supervisor' | string;
  text: string;
}

export interface Session {
  csrf_token: string;
}

export interface ApiErrorBody {
  code: string;
  message: string;
  details?: unknown;
}

export interface DaemonEvent {
  type: string;
  site_id?: string;
  message?: string;
  created_at?: string;
}
