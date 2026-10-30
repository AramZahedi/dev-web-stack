import { API_BASE, getSession, isMockMode, request, servicePath, sitePath } from './api';
import { ApiError } from './errors';
import { mockDatabaseApi } from './databaseMock';
import type {
  CreateDatabaseBackupRequest,
  CreateDatabaseRequest,
  CreateDatabaseUserRequest,
  CreateSiteDatabaseRequest,
  Database,
  DatabaseBackup,
  DatabaseJob,
  DatabaseLink,
  DatabaseSecret,
  DatabaseUser,
  DeleteDatabaseUserRequest,
  DropDatabaseRequest,
  ImportDatabaseRequest,
  LinkDatabaseRequest,
  RenameDatabaseRequest,
  RestoreDatabaseBackupRequest,
  SiteDatabaseResult,
  UpdateDatabaseUserRequest,
} from './databaseTypes';

const dbPath = (serviceID: string, suffix = '') => `${servicePath(serviceID)}/databases${suffix}`;
const userPath = (serviceID: string, suffix = '') => `${servicePath(serviceID)}/users${suffix}`;
const enc = encodeURIComponent;

async function download(path: string): Promise<Blob> {
  const response = await fetch(`${API_BASE}${path}`, { credentials: 'same-origin' });
  if (!response.ok) throw new ApiError('DOWNLOAD_FAILED', 'Could not download the database file.', response.status);
  return response.blob();
}

export async function listServiceDatabases(serviceID: string): Promise<{ databases: Database[] }> {
  if (isMockMode) return mockDatabaseApi.listServiceDatabases(serviceID);
  return request<{ databases: Database[] }>(dbPath(serviceID));
}

export async function createServiceDatabase(serviceID: string, body: CreateDatabaseRequest): Promise<Database> {
  if (isMockMode) return mockDatabaseApi.createServiceDatabase(serviceID, body);
  return request<Database>(dbPath(serviceID), { method: 'POST', body });
}

export async function getServiceDatabase(serviceID: string, name: string): Promise<Database> {
  if (isMockMode) return mockDatabaseApi.getServiceDatabase(serviceID, name);
  return request<Database>(dbPath(serviceID, `/${enc(name)}`));
}

export async function renameServiceDatabase(serviceID: string, name: string, body: RenameDatabaseRequest): Promise<Database> {
  if (isMockMode) return mockDatabaseApi.renameServiceDatabase(serviceID, name, body);
  return request<Database>(dbPath(serviceID, `/${enc(name)}`), { method: 'PATCH', body });
}

export async function dropServiceDatabase(serviceID: string, name: string, body: DropDatabaseRequest): Promise<void> {
  if (isMockMode) return mockDatabaseApi.dropServiceDatabase(serviceID, name, body);
  return request<void>(dbPath(serviceID, `/${enc(name)}`), { method: 'DELETE', body, voidResponse: true });
}

export async function listServiceUsers(serviceID: string): Promise<{ users: DatabaseUser[] }> {
  if (isMockMode) return mockDatabaseApi.listServiceUsers(serviceID);
  return request<{ users: DatabaseUser[] }>(userPath(serviceID));
}

export async function createServiceUser(serviceID: string, body: CreateDatabaseUserRequest): Promise<DatabaseUser> {
  if (isMockMode) return mockDatabaseApi.createServiceUser(serviceID, body);
  return request<DatabaseUser>(userPath(serviceID), { method: 'POST', body });
}

export async function updateServiceUser(serviceID: string, userID: string, body: UpdateDatabaseUserRequest): Promise<DatabaseUser> {
  if (isMockMode) return mockDatabaseApi.updateServiceUser(serviceID, userID, body);
  return request<DatabaseUser>(userPath(serviceID, `/${enc(userID)}`), { method: 'PATCH', body });
}

export async function deleteServiceUser(serviceID: string, userID: string, body: DeleteDatabaseUserRequest): Promise<void> {
  if (isMockMode) return mockDatabaseApi.deleteServiceUser(serviceID, userID, body);
  return request<void>(userPath(serviceID, `/${enc(userID)}`), { method: 'DELETE', body, voidResponse: true });
}

export async function setServiceUserPassword(serviceID: string, userID: string): Promise<DatabaseSecret> {
  if (isMockMode) return mockDatabaseApi.setServiceUserPassword(serviceID, userID);
  return request<DatabaseSecret>(userPath(serviceID, `/${enc(userID)}/password`), { method: 'POST' });
}

export async function revealServiceUserPassword(serviceID: string, userID: string): Promise<DatabaseSecret> {
  if (isMockMode) return mockDatabaseApi.revealServiceUserPassword(serviceID, userID);
  return request<DatabaseSecret>(userPath(serviceID, `/${enc(userID)}/reveal`), { method: 'POST' });
}

export async function listServiceBackups(serviceID: string): Promise<{ backups: DatabaseBackup[] }> {
  if (isMockMode) return mockDatabaseApi.listServiceBackups(serviceID);
  return request<{ backups: DatabaseBackup[] }>(`${servicePath(serviceID)}/backups`);
}

export async function createServiceBackup(serviceID: string, database: string, body: CreateDatabaseBackupRequest = {}): Promise<DatabaseJob> {
  if (isMockMode) return mockDatabaseApi.createServiceBackup(serviceID, database, body);
  return request<DatabaseJob>(dbPath(serviceID, `/${enc(database)}/backups`), { method: 'POST', body });
}

export async function deleteServiceBackup(serviceID: string, backupID: string): Promise<void> {
  if (isMockMode) return mockDatabaseApi.deleteServiceBackup(serviceID, backupID);
  return request<void>(`${servicePath(serviceID)}/backups/${enc(backupID)}`, { method: 'DELETE', voidResponse: true });
}

export async function restoreServiceBackup(serviceID: string, backupID: string, body: RestoreDatabaseBackupRequest): Promise<DatabaseJob> {
  if (isMockMode) return mockDatabaseApi.restoreServiceBackup(serviceID, backupID, body);
  return request<DatabaseJob>(`${servicePath(serviceID)}/backups/${enc(backupID)}/restore`, { method: 'POST', body });
}

export async function downloadServiceBackup(serviceID: string, backupID: string): Promise<Blob> {
  if (isMockMode) return mockDatabaseApi.downloadServiceBackup(serviceID, backupID);
  return download(`${servicePath(serviceID)}/backups/${enc(backupID)}/download`);
}

export async function importServiceDatabase(
  serviceID: string,
  database: string,
  file: File,
  body: ImportDatabaseRequest,
): Promise<DatabaseJob> {
  if (isMockMode) return mockDatabaseApi.importServiceDatabase(serviceID, database, file, body);
  const session = await getSession();
  const form = new FormData();
  form.append('file', file);
  if (body.mode) form.append('mode', body.mode);
  if (body.confirm) form.append('confirm', body.confirm);
  if (body.pre_import_backup) form.append('pre_import_backup', 'true');
  const response = await fetch(`${API_BASE}${dbPath(serviceID, `/${enc(database)}/import`)}`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'X-CSRF-Token': session.csrf_token },
    body: form,
  });
  if (!response.ok) throw new ApiError('IMPORT_FAILED', 'Could not import the database file.', response.status);
  return (await response.json()) as DatabaseJob;
}

export async function exportServiceDatabase(serviceID: string, database: string): Promise<Blob> {
  if (isMockMode) return mockDatabaseApi.exportServiceDatabase(serviceID, database);
  return download(dbPath(serviceID, `/${enc(database)}/export`));
}

export async function getDatabaseJob(jobID: string): Promise<DatabaseJob> {
  if (isMockMode) return mockDatabaseApi.getDatabaseJob(jobID);
  return request<DatabaseJob>(`/jobs/${enc(jobID)}`);
}

export async function cancelDatabaseJob(jobID: string): Promise<void> {
  if (isMockMode) return mockDatabaseApi.cancelDatabaseJob(jobID);
  return request<void>(`/jobs/${enc(jobID)}`, { method: 'DELETE', voidResponse: true });
}

export async function listSiteDatabases(ref: string): Promise<{ links: DatabaseLink[] }> {
  if (isMockMode) return mockDatabaseApi.listSiteDatabases(ref);
  return request<{ links: DatabaseLink[] }>(`${sitePath(ref)}/databases`);
}

export async function linkSiteDatabase(ref: string, body: LinkDatabaseRequest): Promise<DatabaseLink> {
  if (isMockMode) return mockDatabaseApi.linkSiteDatabase(ref, body);
  return request<DatabaseLink>(`${sitePath(ref)}/databases`, { method: 'POST', body });
}

export async function unlinkSiteDatabase(ref: string, linkID: string): Promise<void> {
  if (isMockMode) return mockDatabaseApi.unlinkSiteDatabase(ref, linkID);
  return request<void>(`${sitePath(ref)}/databases/${enc(linkID)}`, { method: 'DELETE', voidResponse: true });
}

export async function createSiteDatabase(ref: string, body: CreateSiteDatabaseRequest): Promise<SiteDatabaseResult> {
  if (isMockMode) return mockDatabaseApi.createSiteDatabase(ref, body);
  return request<SiteDatabaseResult>(`${sitePath(ref)}/databases`, { method: 'POST', body });
}
