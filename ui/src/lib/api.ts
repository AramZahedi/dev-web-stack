import { ApiError } from './errors';
import { mockApi } from './mock';
import type {
  CatalogEntry,
  CreateSiteRequest,
  DaemonEvent,
  DoctorReport,
  FsListing,
  Health,
  LogLine,
  RuntimeBuild,
  Session,
  Settings,
  Site,
  TrustTarget,
  UpdateSiteRequest,
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
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE';
  body?: unknown;
  mutation?: boolean;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET';
  const mutation = options.mutation ?? method !== 'GET';
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (options.body !== undefined) headers['Content-Type'] = 'application/json';
  if (mutation) headers['X-CSRF-Token'] = await fetchSession();

  let response: Response;
  try {
    response = await fetch(`${API_BASE}${path}`, {
      method,
      credentials: 'same-origin',
      headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
    });
  } catch (error) {
    throw networkError(error);
  }

  if (!response.ok) throw await readError(response);
  if (response.status === 204) return undefined as T;
  const contentType = response.headers.get('content-type') ?? '';
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
  await request<void>(sitePath(ref), { method: 'DELETE' });
}

export async function startSite(ref: string): Promise<void> {
  if (isMockMode) return mockApi.startSite(ref);
  await request<void>(`${sitePath(ref)}/start`, { method: 'POST' });
}

export async function stopSite(ref: string): Promise<void> {
  if (isMockMode) return mockApi.stopSite(ref);
  await request<void>(`${sitePath(ref)}/stop`, { method: 'POST' });
}

export async function restartSite(ref: string): Promise<void> {
  if (isMockMode) return mockApi.restartSite(ref);
  await request<void>(`${sitePath(ref)}/restart`, { method: 'POST' });
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
  await request<void>(`/runtimes/${encodeURIComponent(buildID)}`, { method: 'DELETE' });
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
  return request<FsListing>(`/fs?path=${encodeURIComponent(path)}`);
}

export async function getSettings(): Promise<Settings> {
  if (isMockMode) return mockApi.settings();
  return request<Settings>('/settings');
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
