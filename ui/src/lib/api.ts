import { ApiError } from './errors';
import { mockApi } from './mock';
import type {
  BuildLogs,
  BuildResult,
  CatalogEntry,
  CreateSiteRequest,
  DaemonEvent,
  DoctorReport,
  FsListing,
  GlobalIni,
  Health,
  ListServicesResponse,
  LogLine,
  PHPChange,
  PHPDefaults,
  PHPDefaultsResult,
  PHPExecResult,
  PHPExtension,
  BenchmarkRequest,
  BenchmarkRun,
  PerformanceApplyResult,
  PerformancePreview,
  PerformanceProfile,
  PerformanceResetResult,
  PerformanceState,
  PerformanceValues,
  PHPInfo,
  PHPMigrationRequest,
  PHPMigrationResult,
  PHPDefaultStatus,
  RuntimeBuild,
  ServiceConfigApplyRequest,
  ServiceConfigContent,
  ServiceConfigDiff,
  ServiceConfigList,
  ServiceConfigResult,
  ServiceCredentials,
  ServiceLogsResponse,
  ServiceUninstallResult,
  ServiceView,
  Session,
  Settings,
  SettingsUpdate,
  TerminalConnection,
  TerminalHandlers,
  TerminalSession,
  SettingsUpdateResult,
  ServingMode,
  Site,
  ToolsResponse,
  ToolStatus,
  TrustTarget,
  UpdateSiteRequest,
  VhostPreview,
  VhostStatus,
} from './types';

export const API_BASE = '/api/v1';

const configuredMode = import.meta.env.VITE_API_MODE as string | undefined;

/**
 * Mock mode is explicit with VITE_API_MODE=mock, or the dev-server default when
 * the variable is unset. Production builds default to the real API. Tests use
 * the real client so fetch can be asserted.
 */
export const isMockMode =
  configuredMode === 'mock' ||
  (configuredMode === undefined && import.meta.env.DEV && import.meta.env.MODE !== 'test');

let csrfToken: string | null = null;
let sessionRequest: Promise<string> | null = null;

export function resetApiClientForTests(): void {
  csrfToken = null;
  sessionRequest = null;
}

function networkError(error: unknown): ApiError {
  if (error instanceof ApiError) return error;
  const message =
    error instanceof Error && error.message
      ? error.message
      : 'Cannot reach the devstack daemon. Is it running?';
  return new ApiError('NETWORK_ERROR', message, 0);
}

async function readError(response: Response): Promise<ApiError> {
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    body = null;
  }
  if (body && typeof body === 'object') {
    const envelope = body as { code?: unknown; message?: unknown; details?: unknown };
    return new ApiError(
      typeof envelope.code === 'string' ? envelope.code : 'INTERNAL',
      typeof envelope.message === 'string' ? envelope.message : response.statusText,
      response.status,
      envelope.details,
    );
  }
  return new ApiError('INTERNAL', response.statusText || 'Request failed.', response.status);
}

async function fetchSession(): Promise<string> {
  if (csrfToken) return csrfToken;
  if (!sessionRequest) {
    sessionRequest = (async () => {
      let response: Response;
      try {
        response = await fetch(`${API_BASE}/session`, {
          method: 'GET',
          credentials: 'same-origin',
          headers: { Accept: 'application/json' },
        });
      } catch (error) {
        throw networkError(error);
      }
      if (!response.ok) throw await readError(response);
      let body: Session;
      try {
        body = (await response.json()) as Session;
      } catch {
        throw new ApiError('INVALID_SESSION', 'The daemon returned an invalid session response.', response.status);
      }
      if (!body?.csrf_token) {
        throw new ApiError('INVALID_SESSION', 'The daemon did not return a CSRF token.', response.status);
      }
      csrfToken = body.csrf_token;
      return csrfToken;
    })().finally(() => {
      sessionRequest = null;
    });
  }
  return sessionRequest;
}

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  body?: unknown;
  mutation?: boolean;
  /** Endpoints that return no meaningful JSON payload (lifecycle actions, deletes). */
  voidResponse?: boolean;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET';
  const mutation = options.mutation ?? method !== 'GET';
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (options.body !== undefined) headers['Content-Type'] = 'application/json';
  if (mutation) headers['X-CSRF-Token'] = await fetchSession();

  const send = async (): Promise<Response> => {
    try {
      return await fetch(`${API_BASE}${path}`, {
        method,
        credentials: 'same-origin',
        headers,
        body: options.body === undefined ? undefined : JSON.stringify(options.body),
      });
    } catch (error) {
      throw networkError(error);
    }
  };

  let response = await send();
  if (mutation && response.status === 403) {
    const error = await readError(response);
    // A daemon restart changes the CSRF secret. Refresh once, then retry the
    // mutation; do not retry ordinary permission 403s.
    if (error.code === 'FORBIDDEN' && /csrf|session/i.test(error.message)) {
      csrfToken = null;
      sessionRequest = null;
      headers['X-CSRF-Token'] = await fetchSession();
      response = await send();
      if (!response.ok) throw await readError(response);
    } else {
      throw error;
    }
  } else if (!response.ok) {
    throw await readError(response);
  }
  if (response.status === 202 || response.status === 204) return undefined as T;
  const contentType = response.headers.get('content-type') ?? '';
  if (options.voidResponse) {
    // Some proxies/daemons return 200 with an empty body for accepted actions.
    const text = await response.text();
    if (!text.trim() || contentType.includes('application/json')) return undefined as T;
    throw new ApiError(
      'INVALID_RESPONSE',
      'The daemon returned an unexpected response for this action.',
      response.status,
    );
  }
  if (!contentType.includes('application/json')) {
    throw new ApiError(
      'INVALID_RESPONSE',
      'The daemon returned a non-JSON response. Is the API server running?',
      response.status,
    );
  }
  return (await response.json()) as T;
}

function sitePath(ref: string): string {
  return `/sites/${encodeURIComponent(ref)}`;
}

export async function getSession(): Promise<Session> {
  if (isMockMode) return mockApi.getSession();
  const token = await fetchSession();
  return { csrf_token: token };
}

export async function health(): Promise<Health> {
  if (isMockMode) return mockApi.health();
  return request<Health>('/health');
}

export async function listSites(): Promise<Site[]> {
  if (isMockMode) return mockApi.listSites();
  return request<Site[]>('/sites');
}

export async function getSite(ref: string): Promise<Site> {
  if (isMockMode) return mockApi.getSite(ref);
  return request<Site>(sitePath(ref));
}

export async function createSite(body: CreateSiteRequest): Promise<Site> {
  if (isMockMode) return mockApi.createSite(body);
  return request<Site>('/sites', { method: 'POST', body });
}

export async function updateSite(ref: string, body: UpdateSiteRequest): Promise<Site> {
  if (isMockMode) return mockApi.updateSite(ref, body);
  return request<Site>(sitePath(ref), { method: 'PATCH', body });
}

export async function deleteSite(ref: string): Promise<void> {
  if (isMockMode) return mockApi.deleteSite(ref);
  await request<void>(sitePath(ref), { method: 'DELETE', voidResponse: true });
}

export async function startSite(ref: string): Promise<void> {
  if (isMockMode) return mockApi.startSite(ref);
  await request<void>(`${sitePath(ref)}/start`, { method: 'POST', voidResponse: true });
}

export async function stopSite(ref: string): Promise<void> {
  if (isMockMode) return mockApi.stopSite(ref);
  await request<void>(`${sitePath(ref)}/stop`, { method: 'POST', voidResponse: true });
}

export async function restartSite(ref: string): Promise<void> {
  if (isMockMode) return mockApi.restartSite(ref);
  await request<void>(`${sitePath(ref)}/restart`, { method: 'POST', voidResponse: true });
}

export async function listRuntimes(): Promise<RuntimeBuild[]> {
  if (isMockMode) return mockApi.listRuntimes();
  return request<RuntimeBuild[]>('/runtimes');
}

export async function availableRuntimes(): Promise<CatalogEntry[]> {
  if (isMockMode) return mockApi.availableRuntimes();
  return request<CatalogEntry[]>('/runtimes/available');
}

export async function installRuntime(id: string): Promise<RuntimeBuild> {
  if (isMockMode) return mockApi.installRuntime(id);
  return request<RuntimeBuild>('/runtimes/install', { method: 'POST', body: { id } });
}

export async function removeRuntime(buildID: string): Promise<void> {
  if (isMockMode) return mockApi.removeRuntime(buildID);
  await request<void>(`/runtimes/${encodeURIComponent(buildID)}`, {
    method: 'DELETE',
    voidResponse: true,
  });
}

// Older daemons omit the container runtime fields. Normalize them so the
// dashboard can rely on the ServiceView contract without optional guards.
function normalizeService(service: ServiceView): ServiceView {
  const state = service.state === 'not-installed' ? 'not_installed' : service.state;
  return {
    ...service,
    state,
    health: service.health ?? '',
    container_id: service.container_id ?? '',
    installed: service.installed ?? (state !== 'not_installed'),
  };
}

export async function listServices(): Promise<ServiceView[]> {
  if (isMockMode) return mockApi.listServices();
  const res = await request<ListServicesResponse>('/services');
  return (res.services ?? []).map(normalizeService);
}

export async function getService(id: string): Promise<ServiceView> {
  if (isMockMode) return mockApi.getService(id);
  return normalizeService(await request<ServiceView>(`/services/${encodeURIComponent(id)}`));
}

function servicePath(id: string): string {
  return `/services/${encodeURIComponent(id)}`;
}

async function serviceAction(id: string, action: string): Promise<void> {
  await request<void>(`${servicePath(id)}/${action}`, { method: 'POST', voidResponse: true });
}

export async function installService(id: string): Promise<void> {
  if (isMockMode) return mockApi.installService(id);
  await serviceAction(id, 'install');
}

export async function activateService(id: string): Promise<void> {
  if (isMockMode) return mockApi.activateService(id);
  await serviceAction(id, 'activate');
}

export async function deactivateService(id: string): Promise<void> {
  if (isMockMode) return mockApi.deactivateService(id);
  await serviceAction(id, 'deactivate');
}

export async function restartService(id: string): Promise<void> {
  if (isMockMode) return mockApi.restartService(id);
  await serviceAction(id, 'restart');
}

export async function uninstallService(
  id: string,
  deleteData = false,
): Promise<ServiceUninstallResult> {
  if (isMockMode) return mockApi.uninstallService(id, deleteData);
  return request<ServiceUninstallResult>(servicePath(id), {
    method: 'DELETE',
    body: { delete_data: deleteData },
  });
}

export async function serviceLogs(id: string, tail = 200): Promise<string> {
  if (isMockMode) return mockApi.serviceLogs(id, tail);
  const response = await request<ServiceLogsResponse>(
    `${servicePath(id)}/logs?tail=${encodeURIComponent(String(tail))}`,
  );
  return response.logs ?? '';
}

export async function revealServiceCredentials(id: string): Promise<ServiceCredentials> {
  if (isMockMode) return mockApi.revealServiceCredentials(id);
  return request<ServiceCredentials>(`${servicePath(id)}/credentials/reveal`, {
    method: 'POST',
  });
}

export async function rotateServiceCredentials(id: string): Promise<ServiceCredentials> {
  if (isMockMode) return mockApi.rotateServiceCredentials(id);
  return request<ServiceCredentials>(`${servicePath(id)}/credentials/rotate`, {
    method: 'POST',
  });
}

export async function listTools(): Promise<ToolStatus[]> {
  if (isMockMode) return mockApi.listTools();
  const response = await request<ToolsResponse>('/tools');
  return response.tools ?? [];
}

export async function installTool(id: string, force = false): Promise<ToolStatus> {
  if (isMockMode) return mockApi.installTool(id, force);
  return request<ToolStatus>(`/tools/${encodeURIComponent(id)}/install`, {
    method: 'POST',
    body: { force },
  });
}

export async function removeTool(id: string): Promise<ToolStatus> {
  if (isMockMode) return mockApi.removeTool(id);
  return request<ToolStatus>(`/tools/${encodeURIComponent(id)}/remove`, { method: 'POST' });
}

export async function getPHPDefault(): Promise<PHPDefaultStatus> {
  if (isMockMode) return mockApi.getPHPDefault();
  return request<PHPDefaultStatus>('/php/default');
}

export async function setPHPDefault(value: string, force = false): Promise<PHPDefaultStatus> {
  if (isMockMode) return mockApi.setPHPDefault(value, force);
  return request<PHPDefaultStatus>('/php/default', {
    method: 'PUT',
    body: { value, force },
  });
}

export async function clearPHPDefault(): Promise<PHPDefaultStatus> {
  if (isMockMode) return mockApi.clearPHPDefault();
  return request<PHPDefaultStatus>('/php/default', {
    method: 'PUT',
    body: { clear: true },
  });
}

export async function getGlobalIni(): Promise<GlobalIni> {
  if (isMockMode) return mockApi.getGlobalIni();
  return request<GlobalIni>('/php/default/ini');
}

export async function saveGlobalIni(content: string): Promise<GlobalIni> {
  if (isMockMode) return mockApi.saveGlobalIni(content);
  return request<GlobalIni>('/php/default/ini', { method: 'PUT', body: { content } });
}

export async function buildSite(ref: string): Promise<BuildResult> {
  if (isMockMode) return mockApi.buildSite(ref);
  return request<BuildResult>(`${sitePath(ref)}/build`, { method: 'POST' });
}

export async function getBuildLogs(ref: string, tail = 200): Promise<BuildLogs> {
  if (isMockMode) return mockApi.getBuildLogs(ref, tail);
  return request<BuildLogs>(
    `${sitePath(ref)}/build/logs?tail=${encodeURIComponent(String(tail))}`,
  );
}

export async function getPHPInfo(ref: string): Promise<PHPInfo> {
  if (isMockMode) return mockApi.getPHPInfo(ref);
  return request<PHPInfo>(`${sitePath(ref)}/php`);
}

export async function applyPHP(ref: string, change: PHPChange): Promise<Site> {
  if (isMockMode) return mockApi.applyPHP(ref, change);
  return request<Site>(`${sitePath(ref)}/php`, { method: 'PUT', body: change });
}

export async function phpExtensions(version: string): Promise<PHPExtension[]> {
  if (isMockMode) return mockApi.phpExtensions(version);
  return request<PHPExtension[]>(`/php/extensions?version=${encodeURIComponent(version)}`);
}

export async function getPHPDefaults(version: string): Promise<PHPDefaults> {
  if (isMockMode) return mockApi.getPHPDefaults(version);
  return request<PHPDefaults>(`/php/defaults/${encodeURIComponent(version)}`);
}

export async function savePHPDefaults(
  version: string,
  defaults: Pick<PHPDefaults, 'ini' | 'extensions'>,
): Promise<PHPDefaultsResult> {
  if (isMockMode) return mockApi.savePHPDefaults(version, defaults);
  return request<PHPDefaultsResult>(`/php/defaults/${encodeURIComponent(version)}`, {
    method: 'PUT',
    body: defaults,
  });
}

export async function migratePHP(change: PHPMigrationRequest): Promise<PHPMigrationResult[]> {
  if (isMockMode) return mockApi.migratePHP(change);
  return request<PHPMigrationResult[]>('/php/migrate', { method: 'POST', body: change });
}

export async function phpExec(ref: string, argv: string[]): Promise<PHPExecResult> {
  if (isMockMode) return mockApi.phpExec(ref, argv);
  return request<PHPExecResult>(`${sitePath(ref)}/php/exec`, {
    method: 'POST',
    body: { argv },
  });
}

export async function getDoctor(): Promise<DoctorReport> {
  if (isMockMode) return mockApi.doctor();
  return request<DoctorReport>('/doctor');
}

export async function runDoctor(): Promise<DoctorReport> {
  if (isMockMode) return mockApi.runDoctor();
  return request<DoctorReport>('/doctor/run', { method: 'POST' });
}

export async function repairDoctor(): Promise<DoctorReport> {
  if (isMockMode) return mockApi.repairDoctor();
  return request<DoctorReport>('/doctor/repair', { method: 'POST' });
}

export async function getTrust(): Promise<TrustTarget[]> {
  if (isMockMode) return mockApi.trust();
  return request<TrustTarget[]>('/trust');
}

export async function repairTrust(): Promise<TrustTarget[]> {
  if (isMockMode) return mockApi.repairTrust();
  return request<TrustTarget[]>('/trust/repair', { method: 'POST' });
}

export async function removeTrust(): Promise<TrustTarget[]> {
  if (isMockMode) return mockApi.removeTrust();
  return request<TrustTarget[]>('/trust', { method: 'DELETE' });
}

export async function browseFs(path: string): Promise<FsListing> {
  if (isMockMode) return mockApi.browseFs(path);
  // /fs is session-gated even though it is a GET. Ensure the session cookie is
  // present, and recover once if the daemon restarted between calls.
  await fetchSession();
  try {
    return await request<FsListing>(`/fs?path=${encodeURIComponent(path)}`);
  } catch (error) {
    if (!(error instanceof ApiError) || error.status !== 403) throw error;
    csrfToken = null;
    sessionRequest = null;
    await fetchSession();
    return request<FsListing>(`/fs?path=${encodeURIComponent(path)}`);
  }
}

export async function listServiceConfig(id: string): Promise<ServiceConfigList> {
  if (isMockMode) return mockApi.listServiceConfig(id);
  const response = await request<ServiceConfigList | ServiceConfigList['files']>(
    `/services/${encodeURIComponent(id)}/config`,
  );
  if (Array.isArray(response)) {
    return { installed: true, files: response };
  }
  return { installed: response.installed ?? true, files: response.files ?? [] };
}

export async function getServiceConfigContent(
  id: string,
  path: string,
): Promise<ServiceConfigContent> {
  if (isMockMode) return mockApi.getServiceConfigContent(id, path);
  const response = await request<ServiceConfigContent | string>(
    `/services/${encodeURIComponent(id)}/config/content?path=${encodeURIComponent(path)}`,
  );
  if (typeof response === 'string') return { path, content: response };
  return response;
}

export async function diffServiceConfig(
  id: string,
  path: string,
  content: string,
): Promise<ServiceConfigDiff> {
  if (isMockMode) return mockApi.diffServiceConfig(id, path, content);
  return request<ServiceConfigDiff>(`/services/${encodeURIComponent(id)}/config/diff`, {
    method: 'POST',
    body: { path, content },
  });
}

export async function applyServiceConfig(
  id: string,
  body: ServiceConfigApplyRequest,
): Promise<ServiceConfigResult> {
  if (isMockMode) return mockApi.applyServiceConfig(id, body);
  return request<ServiceConfigResult>(`/services/${encodeURIComponent(id)}/config/apply`, {
    method: 'POST',
    body,
  });
}

export async function resetServiceConfig(
  id: string,
  path: string,
): Promise<ServiceConfigResult> {
  if (isMockMode) return mockApi.resetServiceConfig(id, path);
  return request<ServiceConfigResult>(`/services/${encodeURIComponent(id)}/config/reset`, {
    method: 'POST',
    body: { path },
  });
}

export async function getPerformance(): Promise<PerformanceState> {
  if (isMockMode) return mockApi.getPerformance();
  return request<PerformanceState>('/performance');
}

export async function setPerformanceProfile(
  profile: PerformanceProfile,
  custom?: PerformanceValues,
): Promise<PerformanceState> {
  if (isMockMode) return mockApi.setPerformanceProfile(profile, custom);
  return request<PerformanceState>('/performance/profile', {
    method: 'PUT',
    body: custom === undefined ? { profile } : { profile, custom },
  });
}

export async function previewPerformance(): Promise<PerformancePreview> {
  if (isMockMode) return mockApi.previewPerformance();
  return request<PerformancePreview>('/performance/preview');
}

export async function applyPerformance(
  hostTuning: boolean,
): Promise<PerformanceApplyResult> {
  if (isMockMode) return mockApi.applyPerformance(hostTuning);
  return request<PerformanceApplyResult>('/performance/apply', {
    method: 'POST',
    body: { confirm: true, host_tuning: hostTuning },
  });
}

export async function resetPerformance(): Promise<PerformanceResetResult> {
  if (isMockMode) return mockApi.resetPerformance();
  return request<PerformanceResetResult>('/performance/reset', {
    method: 'POST',
    body: { confirm: true },
  });
}

export async function listBenchmarks(): Promise<BenchmarkRun[]> {
  if (isMockMode) return mockApi.listBenchmarks();
  const response = await request<BenchmarkRun[] | { benchmarks?: BenchmarkRun[] }>('/performance/benchmarks');
  return Array.isArray(response) ? response : response.benchmarks ?? [];
}

export async function getBenchmark(id: string): Promise<BenchmarkRun> {
  if (isMockMode) return mockApi.getBenchmark(id);
  return request<BenchmarkRun>(`/performance/benchmarks/${encodeURIComponent(id)}`);
}

export async function runBenchmark(requestBody: BenchmarkRequest): Promise<BenchmarkRun> {
  if (isMockMode) return mockApi.runBenchmark(requestBody);
  return request<BenchmarkRun>('/performance/benchmarks', {
    method: 'POST',
    body: requestBody,
  });
}

export async function createTerminalSession(ref: string): Promise<TerminalSession> {
  if (isMockMode) return mockApi.createTerminalSession(ref);
  return request<TerminalSession>(`${sitePath(ref)}/terminal/session`, {
    method: 'POST',
    body: {},
  });
}

export async function openTerminal(
  ref: string,
  handlers: TerminalHandlers,
): Promise<TerminalConnection> {
  if (isMockMode) return mockApi.openTerminal(ref, handlers);
  handlers.onStatus('connecting');
  const session = await createTerminalSession(ref);
  const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const url = `${scheme}//${window.location.host}${API_BASE}${sitePath(ref)}/terminal?ticket=${encodeURIComponent(session.ticket)}`;
  const socket = new WebSocket(url);
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    socket.close();
  };
  socket.onopen = () => handlers.onStatus('connected');
  socket.onmessage = (event) => {
    try {
      const message = JSON.parse(String(event.data)) as {
        type?: string;
        data?: string;
        code?: number;
        message?: string;
      };
      if (message.type === 'output' && typeof message.data === 'string') {
        handlers.onOutput(message.data);
      } else if (message.type === 'exit') {
        handlers.onExit(message.code ?? 0);
        closed = true;
        socket.close();
      } else if (message.type === 'error') {
        handlers.onStatus('error');
        handlers.onError?.('TERMINAL_ERROR', message.message ?? 'The terminal session reported an error.');
      }
    } catch {
      // Raw output is tolerated so older daemons still work.
      if (typeof event.data === 'string') handlers.onOutput(event.data);
    }
  };
  socket.onerror = () => handlers.onStatus('error');
  socket.onclose = () => {
    if (!closed) handlers.onStatus('closed');
    closed = true;
  };
  return {
    send(data) {
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: 'input', data }));
      }
    },
    resize(cols, rows) {
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: 'resize', cols, rows }));
      }
    },
    close,
  };
}

export async function getSettings(): Promise<Settings> {
  if (isMockMode) return mockApi.settings();
  const settings = await request<Settings>('/settings');
  // Older daemons predate serving_provider; native is the compatible default.
  return { ...settings, serving_provider: settings.serving_provider ?? 'native' };
}

export async function updateSettings(update: SettingsUpdate): Promise<SettingsUpdateResult> {
  if (isMockMode) return mockApi.updateSettings(update);
  const settings = await request<SettingsUpdateResult>('/settings', {
    method: 'PATCH',
    body: update,
  });
  return { ...settings, serving_provider: settings.serving_provider ?? 'native' };
}

export async function getVhostStatus(): Promise<VhostStatus> {
  if (isMockMode) return mockApi.getVhostStatus();
  return request<VhostStatus>('/vhosts/status');
}

export async function setServingMode(mode: ServingMode): Promise<VhostStatus> {
  if (isMockMode) return mockApi.setServingMode(mode);
  return request<VhostStatus>('/vhosts/mode', { method: 'POST', body: { mode } });
}

export async function syncVhosts(): Promise<VhostStatus> {
  if (isMockMode) return mockApi.syncVhosts();
  return request<VhostStatus>('/vhosts/sync', { method: 'POST' });
}

export async function previewVhost(
  site: string,
  server: 'nginx' | 'apache',
): Promise<VhostPreview> {
  if (isMockMode) return mockApi.previewVhost(site, server);
  return request<VhostPreview>(
    `/vhosts/preview?site=${encodeURIComponent(site)}&server=${server}`,
  );
}

export interface StreamHandlers {
  onLine: (line: LogLine) => void;
  onStatus?: (status: 'live' | 'error') => void;
}

export function openLogStream(ref: string, handlers: StreamHandlers): () => void {
  if (isMockMode) return mockApi.openLogStream(ref, handlers.onLine, handlers.onStatus);

  const source = new EventSource(`${API_BASE}${sitePath(ref)}/logs/stream`);
  source.onopen = () => handlers.onStatus?.('live');
  source.onmessage = (event) => {
    try {
      const line = JSON.parse(event.data) as LogLine;
      if (line && typeof line.text === 'string') handlers.onLine(line);
    } catch {
      // Ignore keepalive or malformed frames; the stream stays open.
    }
  };
  source.onerror = () => handlers.onStatus?.('error');
  return () => source.close();
}

export function openEvents(onEvent: (event: DaemonEvent) => void): () => void {
  if (isMockMode) return mockApi.openEvents(onEvent);

  const source = new EventSource(`${API_BASE}/events`);
  source.onmessage = (event) => {
    try {
      onEvent(JSON.parse(event.data) as DaemonEvent);
    } catch {
      // Ignore keepalives.
    }
  };
  return () => source.close();
}
