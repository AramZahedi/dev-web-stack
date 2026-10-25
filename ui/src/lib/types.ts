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

export type ServingMode = 'builtin' | 'nginx' | 'apache';
export type FrontControllerMode = 'auto' | 'generated' | 'htaccess';
export type ApacheAllowOverride = 'all' | 'fileinfo' | 'none';

export interface BuildProfile {
  node_version?: string;
  command?: string;
  output?: string;
  env?: Record<string, string>;
}

export interface VhostTLSFile {
  name: string;
  mode: number;
  regular: boolean;
}

export interface VhostStatus {
  mode: ServingMode;
  server: string;
  server_version: string;
  bridge_path: string;
  include_present: boolean;
  current_release: string;
  config_ok: boolean;
  last_error?: string;
  last_sync: string;
  tls_dir_mode?: number;
  tls_files?: VhostTLSFile[];
}

export interface VhostPreview {
  server: 'nginx' | 'apache';
  site: string;
  content: string;
}

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
  php_build: string;
  php_extensions?: Record<string, boolean>;
  php_ini?: Record<string, string>;
  php_ini_raw: string;
  php_config_hash: string;
  node_version: string;
  command: string;
  env: Record<string, string>;
  document_root: string;
  front_controller: boolean;
  front_controller_file: string;
  front_controller_mode: FrontControllerMode;
  apache_allow_override: ApacheAllowOverride;
  spa_fallback: boolean;
  spa_fallback_file: string;
  build?: BuildProfile;
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
  php_version?: string;
  php_build?: string;
  php_extensions?: Record<string, boolean>;
  php_ini?: Record<string, string>;
  php_ini_raw?: string;
  front_controller?: boolean;
  front_controller_file?: string;
  front_controller_mode?: FrontControllerMode;
  apache_allow_override?: ApacheAllowOverride;
  spa_fallback_file?: string;
  build?: BuildProfile;
}

export interface UpdateSiteRequest {
  name?: string;
  domain?: string;
  www?: boolean;
  node_version?: string;
  command?: string;
  document_root?: string;
  front_controller?: boolean;
  front_controller_file?: string;
  front_controller_mode?: FrontControllerMode;
  apache_allow_override?: ApacheAllowOverride;
  spa_fallback?: boolean;
  spa_fallback_file?: string;
  build?: BuildProfile;
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
  revision?: number;
  url: string;
  sha256: string;
  size: number;
}

export type PHPExtensionType = 'extension' | 'zend_extension';

/** One extension shipped in the embedded PHP catalog for a version line. */
export interface PHPExtension {
  name: string;
  type: PHPExtensionType;
  so: string;
  default: boolean;
  standard: boolean;
  versions: string[];
  requires?: string[];
}

/** One extension selected after defaults and site overrides are resolved. */
export interface PHPResolvedExtension {
  name: string;
  type: PHPExtensionType;
  so: string;
  required: boolean;
  required_by?: string[];
}

/** An installed PHP build with the line and revision derived from its ID. */
export interface PHPRuntimeBuild extends RuntimeBuild {
  line: string;
  revision: number;
}

export interface PHPDefaults {
  version: string;
  ini: Record<string, string>;
  extensions: Record<string, boolean>;
}

/** A partial PHP configuration change; omitted fields keep their current value. */
export interface PHPChange {
  version?: string;
  build?: string;
  extensions?: Record<string, boolean>;
  ini?: Record<string, string>;
  ini_raw?: string;
}

export interface PHPInfo {
  site: Site;
  build: RuntimeBuild;
  defaults: PHPDefaults;
  extensions: PHPResolvedExtension[];
  available_extensions: PHPExtension[];
  builtins: string[];
  ini: Record<string, string>;
  ini_raw: string;
  generated_ini: string;
  fpm_config: string;
  config_hash: string;
}

export interface PHPMigrationRequest {
  from_version: string;
  to_version: string;
  to_build: string;
}

export interface PHPMigrationResult {
  site: string;
  ok: boolean;
  error?: string;
}

export interface PHPDefaultsResult {
  defaults: PHPDefaults;
  sites: PHPMigrationResult[];
}

export interface PHPExecResult {
  exit_code: number;
  stdout: string;
  stderr: string;
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
  serving_mode: ServingMode;
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
