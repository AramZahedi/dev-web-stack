export type SiteKind = 'static' | 'php' | 'node';
export type SourceMode = 'local' | 'windows' | 'wsl';
export type BackendKind = 'local' | 'wsl';
export type DesiredState = 'running' | 'stopped';
export type SiteStatus =
  | 'stopped'
  | 'starting'
  | 'running'
  | 'stopping'
  | 'crashed'
  | 'degraded'
  | 'error';

export type CheckStatus = 'ok' | 'warn' | 'fail' | 'skipped';

export type ServingMode = 'builtin' | 'nginx' | 'apache';
export type ServingProvider = 'native' | 'docker';
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

export interface SitePHPFPMBudget {
  mode?: 'dynamic' | 'static';
  max_children?: number;
  start_servers?: number;
  min_spare_servers?: number;
  max_spare_servers?: number;
  listen_backlog?: number;
}

export interface Site {
  id: string;
  name: string;
  domain: string;
  www: boolean;
  backend: BackendKind;
  source_mode?: SourceMode;
  host_path: string;
  backend_path: string;
  kind: SiteKind;
  php_version: string;
  php_build: string;
  php_extensions?: Record<string, boolean>;
  php_ini?: Record<string, string>;
  php_ini_raw: string;
  php_config_hash: string;
  php_fpm?: SitePHPFPMBudget;
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
  source_mode?: SourceMode;
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
  serving_provider: ServingProvider;
  wsl_distro: string;
  auto_start_sites: boolean;
  graceful_stop_seconds: number;
  max_request_body_mb: number;
  log_max_mb: number;
  log_keep: number;
  restart_required?: boolean;
  /** Windows fixes the helper/firewall target ports at 8080/8443. */
  public_ports_fixed?: boolean;
  version?: string;
  socket_path?: string;
  pid_path?: string;
  /** Optional convenience field; real daemons may omit it. */
  user_home?: string;
}

export interface SettingsUpdate {
  http_port?: number;
  https_port?: number;
  ui_port?: number;
}

export interface SettingsUpdateResult extends Settings {
  warnings?: string[];
}

export interface ServiceConfigConflict {
  active?: boolean;
  resolution_required?: boolean;
  reason?: string;
  allowed_resolutions?: string[];
}

export interface ServiceConfigFile {
  path: string;
  class: string;
  generated: boolean;
  modified: boolean;
  detached: boolean;
  hash?: string;
  size?: number;
  conflict?: ServiceConfigConflict;
}

export interface ServiceConfigList {
  service?: string;
  installed: boolean;
  files: ServiceConfigFile[];
  validation_pending?: boolean;
}

export interface ServiceConfigContent {
  path: string;
  content: string;
  class?: string;
  generated?: boolean;
  modified?: boolean;
  detached?: boolean;
  hash?: string;
  size?: number;
  conflict?: ServiceConfigConflict;
}

export interface ServiceConfigDiff {
  path: string;
  diff: string;
  current_hash?: string;
  proposed_hash?: string;
  current_size?: number;
  proposed_size?: number;
  status?: ServiceConfigFile;
  conflict?: ServiceConfigConflict;
}

export type ServiceConfigResolution = 'keep' | 'use' | 'merge';

export interface ServiceConfigApplyRequest {
  path: string;
  content: string;
  resolution?: ServiceConfigResolution;
}

export interface ServiceConfigResult {
  service?: string;
  path?: string;
  action?: 'apply' | 'reset';
  changed?: boolean;
  resolution?: string;
  backup_path?: string;
  restored_from?: string;
  detached?: boolean;
  hash?: string;
  size?: number;
  active?: boolean;
  live?: boolean;
  reloaded?: boolean;
  validation_pending?: boolean;
  restart_required?: boolean;
  rolled_back?: boolean;
  message?: string;
  conflict?: ServiceConfigConflict;
}

export type PerformanceProfile = 'balanced' | 'throughput' | 'custom';

export interface PerformanceResources {
  hostname?: string;
  arch?: string;
  cpu_count?: number;
  cpu_quota_cpus?: number;
  memory_total_bytes?: number;
  memory_available_bytes?: number;
  page_size_bytes?: number;
  nofile_soft?: number;
  nofile_hard?: number;
  user?: string;
  group?: string;
  [key: string]: unknown;
}

export interface PerformanceValues {
  [key: string]: unknown;
}

export interface PerformanceStep {
  name: string;
  status: 'ok' | 'skipped' | 'failed' | 'rolled_back' | string;
  detail?: string;
  backup?: string;
}

export interface PerformanceAppliedState {
  profile: PerformanceProfile;
  values?: PerformanceValues;
  applied_at?: string;
  host_tuning?: boolean;
  backup_dir?: string;
  servers?: string[];
  steps?: PerformanceStep[];
}

export interface PerformanceHostTuning {
  available?: boolean;
  managed?: boolean;
  applied?: boolean;
  sysctl_path?: string;
  limits_path?: string;
  backup_dir?: string;
  applied_at?: string;
  profile?: string;
  error?: string;
}

export interface PerformanceState {
  profile: PerformanceProfile;
  custom?: PerformanceValues;
  values: PerformanceValues;
  resources: PerformanceResources;
  applied?: PerformanceAppliedState;
  host_tuning: PerformanceHostTuning;
  active_server?: string;
  warnings?: string[];
}

export interface PerformanceManagedFile {
  path: string;
  content?: string;
  active: boolean;
}

export interface PerformanceHostFile {
  path: string;
  exists: boolean;
  current?: string;
  proposed?: string;
  diff?: string;
}

export interface PerformanceHostPreview {
  opt_in: boolean;
  files: PerformanceHostFile[];
  warnings?: string[];
}

export interface PerformancePreview {
  profile: PerformanceProfile;
  values: PerformanceValues;
  resources?: PerformanceResources;
  active_server?: string;
  nginx?: { main?: string; events?: string; http?: string };
  apache?: { config?: string };
  php?: { site_id: string; pool: string; ini: string }[];
  managed_files: PerformanceManagedFile[];
  host_tuning: PerformanceHostPreview;
  warnings?: string[];
}

export interface PerformanceApplyResult {
  applied: boolean;
  profile: PerformanceProfile;
  applied_at?: string;
  host_tuning: boolean;
  active_server?: string;
  files?: PerformanceManagedFile[];
  steps: PerformanceStep[];
  backup_dir?: string;
  rollback_error?: string;
  warnings?: string[];
}

export interface PerformanceResetResult {
  reset: boolean;
  steps: PerformanceStep[];
  backup_dir?: string;
  rollback_error?: string;
  warnings?: string[];
}

export interface BenchmarkRequest {
  site: string;
  duration_s?: number;
  connections?: number;
}

export interface BenchmarkMeasurement {
  requests: number;
  errors: number;
  requests_per_second: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  duration_seconds: number;
}

export interface BenchmarkRun {
  id: string;
  site: string;
  target: string;
  profile: string;
  status: 'completed' | 'failed' | 'canceled' | string;
  tool: string;
  tool_version: string;
  limitations?: string[];
  duration_s: number;
  connections: number;
  started_at: string;
  finished_at: string;
  measured?: BenchmarkMeasurement | null;
  error?: string;
  directory: string;
  raw_file: string;
  profile_applied_at?: string;
}

export interface TerminalSession {
  ticket: string;
  expires_at: string;
}

export type TerminalStatus = 'connecting' | 'connected' | 'closed' | 'error';

export interface TerminalHandlers {
  onOutput: (text: string) => void;
  onExit: (code: number) => void;
  onStatus: (status: TerminalStatus) => void;
  onError?: (code: string, message: string) => void;
}

export interface TerminalConnection {
  send: (data: string) => void;
  resize: (cols: number, rows: number) => void;
  close: () => void;
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

export type ServiceState =
  | 'not_installed'
  | 'installed'
  | 'installed_but_stopped'
  | 'starting'
  | 'active'
  | 'stopping'
  | 'error';

export type ServiceGroup = 'web' | 'mysql-family' | 'standalone';

export interface ServicePort {
  host: number;
  container: number;
  protocol?: string;
  loopback?: boolean;
}

export interface ServiceView {
  id: string;
  display_name: string;
  group: ServiceGroup;
  state: ServiceState;
  active: boolean;
  installed: boolean;
  health: string;
  container_id: string;
  image: string;
  ports?: ServicePort[];
  last_error?: string;
  config_path?: string;
  data_path?: string;
}

export interface ListServicesResponse {
  services: ServiceView[];
}

/** One database/service credential payload from reveal or rotate. */
export interface ServiceCredentials {
  engine: string;
  username: string;
  password: string;
  database: string;
  host: string;
  port: number;
}

export interface ServiceLogsResponse {
  logs: string;
}

export interface ServiceUninstallResult {
  backup_path?: string;
}

/** One managed native tool reported by the privileged helper. */
export interface ToolStatus {
  id: string;
  installed: boolean;
  managed: boolean;
  wrapper_path?: string;
  target?: string;
  version?: string;
  conflict?: string;
  backup?: string;
}

export interface ToolsResponse {
  tools: ToolStatus[];
}

/** Machine-wide CLI PHP default state. */
export interface PHPDefaultStatus {
  configured?: string;
  build?: RuntimeBuild;
  global_ini: string;
  shim_dir: string;
  shim_installed: boolean;
}

/** The editable machine-wide CLI php.ini. */
export interface GlobalIni {
  path: string;
  content: string;
  build_id?: string;
}

/** Result of one static site build attempt. */
export interface BuildResult {
  site: string;
  success: boolean;
  command: string;
  duration_ms: number;
  log_path?: string;
  output_path?: string;
  restored: boolean;
  snapshot_skipped: boolean;
  error?: string;
  restore_note?: string;
}

/** Bounded tail of the newest site build log. */
export interface BuildLogs {
  site: string;
  path?: string;
  content: string;
}
