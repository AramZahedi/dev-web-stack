import { ApiError } from './errors';
import type {
  CreateDatabaseBackupRequest,
  CreateDatabaseRequest,
  CreateDatabaseUserRequest,
  CreateSiteDatabaseRequest,
  Database,
  DatabaseBackup,
  DatabaseConnection,
  DatabaseEngine,
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

const now = () => new Date().toISOString();
const clone = <T>(value: T): T => structuredClone(value);
const wait = () => new Promise((resolve) => setTimeout(resolve, 20));

function engineFor(serviceID: string): DatabaseEngine {
  if (serviceID === 'postgresql' || serviceID === 'postgres') return 'postgres';
  if (serviceID === 'mariadb') return 'mariadb';
  if (serviceID === 'redis') return 'redis';
  return 'mysql';
}

function portFor(engine: DatabaseEngine): number {
  return engine === 'postgres' ? 5432 : engine === 'redis' ? 6379 : 3306;
}

function connectionFor(link: {
  engine: DatabaseEngine;
  database: string;
  username: string;
  password_managed: boolean;
}): DatabaseConnection {
  const env: Record<string, string> = {
    DB_CONNECTION: link.engine === 'postgres' ? 'pgsql' : link.engine,
    DB_HOST: '127.0.0.1',
    DB_PORT: String(portFor(link.engine)),
    DB_DATABASE: link.database,
    DB_USERNAME: link.username,
  };
  return {
    engine: link.engine,
    host: '127.0.0.1',
    port: portFor(link.engine),
    database: link.database,
    username: link.username,
    password_managed: link.password_managed,
    url: `${link.engine}://${link.username}@127.0.0.1:${portFor(link.engine)}/${link.database}`,
    env,
  };
}

let databases: Database[] = [
  { name: 'app', service_id: 'mysql', engine: 'mysql', size_bytes: 13_000_000, table_count: 12, created_at: now(), linked_sites: [], managed: true },
  { name: 'blog', service_id: 'mysql', engine: 'mysql', size_bytes: 4_200_000, table_count: 7, created_at: now(), linked_sites: [{ id: 'site_blog', name: 'blog' }], managed: true },
  { name: 'devstack', service_id: 'mysql', engine: 'mysql', size_bytes: 1_100_000, table_count: 2, created_at: now(), linked_sites: [], managed: false },
  { name: 'app', service_id: 'postgresql', engine: 'postgres', size_bytes: 8_000_000, table_count: 9, created_at: now(), linked_sites: [], managed: true },
];

let users: DatabaseUser[] = [
  { id: 'usr_app', service_id: 'mysql', engine: 'mysql', username: 'app', host: '%', enabled: true, managed: true, password_managed: true, grants: [{ database: 'app', preset: 'read_write_schema' }], linked_sites: [], created_at: now() },
  { id: 'usr_ext', service_id: 'mysql', engine: 'mysql', username: 'ext', host: '%', enabled: true, managed: false, password_managed: false, grants: [], linked_sites: [] },
  { id: 'usr_pg', service_id: 'postgresql', engine: 'postgres', username: 'app', enabled: true, managed: true, password_managed: true, grants: [{ database: 'app', preset: 'read_write_schema' }], linked_sites: [] },
  { id: 'usr_cache', service_id: 'redis', engine: 'redis', username: 'cache', enabled: true, managed: true, password_managed: true, access_level: 'read-only', grants: [], linked_sites: [] },
  { id: 'usr_default', service_id: 'redis', engine: 'redis', username: 'default', enabled: true, managed: false, password_managed: true, access_level: 'full', grants: [], linked_sites: [] },
];

let backups: DatabaseBackup[] = [
  { id: 'bkp_1', service_id: 'mysql', engine: 'mysql', database: 'app', size_bytes: 2_400_000, sha256: 'abc123', kind: 'manual', status: 'ready', created_at: now() },
];

let jobs: DatabaseJob[] = [];
let links: DatabaseLink[] = [];

export function resetMockDatabaseStateForTests(): void {
  databases = [
    { name: 'app', service_id: 'mysql', engine: 'mysql', size_bytes: 13_000_000, table_count: 12, created_at: now(), linked_sites: [], managed: true },
    { name: 'blog', service_id: 'mysql', engine: 'mysql', size_bytes: 4_200_000, table_count: 7, created_at: now(), linked_sites: [{ id: 'site_blog', name: 'blog' }], managed: true },
    { name: 'devstack', service_id: 'mysql', engine: 'mysql', size_bytes: 1_100_000, table_count: 2, created_at: now(), linked_sites: [], managed: false },
    { name: 'app', service_id: 'postgresql', engine: 'postgres', size_bytes: 8_000_000, table_count: 9, created_at: now(), linked_sites: [], managed: true },
  ];
  users = [
    { id: 'usr_app', service_id: 'mysql', engine: 'mysql', username: 'app', host: '%', enabled: true, managed: true, password_managed: true, grants: [{ database: 'app', preset: 'read_write_schema' }], linked_sites: [], created_at: now() },
    { id: 'usr_ext', service_id: 'mysql', engine: 'mysql', username: 'ext', host: '%', enabled: true, managed: false, password_managed: false, grants: [], linked_sites: [] },
    { id: 'usr_pg', service_id: 'postgresql', engine: 'postgres', username: 'app', enabled: true, managed: true, password_managed: true, grants: [{ database: 'app', preset: 'read_write_schema' }], linked_sites: [] },
    { id: 'usr_cache', service_id: 'redis', engine: 'redis', username: 'cache', enabled: true, managed: true, password_managed: true, access_level: 'read-only', grants: [], linked_sites: [] },
    { id: 'usr_default', service_id: 'redis', engine: 'redis', username: 'default', enabled: true, managed: false, password_managed: true, access_level: 'full', grants: [], linked_sites: [] },
  ];
  backups = [
    { id: 'bkp_1', service_id: 'mysql', engine: 'mysql', database: 'app', size_bytes: 2_400_000, sha256: 'abc123', kind: 'manual', status: 'ready', created_at: now() },
  ];
  jobs = [];
  links = [];
}

function serviceDatabases(serviceID: string): Database[] {
  return databases.filter((database) => database.service_id === serviceID);
}

function serviceUsers(serviceID: string): DatabaseUser[] {
  return users.filter((user) => user.service_id === serviceID);
}

function findUser(serviceID: string, userID: string): DatabaseUser {
  const user = users.find((candidate) => candidate.service_id === serviceID && candidate.id === userID);
  if (!user) throw new ApiError('DATABASE_NOT_FOUND', 'Database user not found.', 404);
  return user;
}

function findDatabase(serviceID: string, name: string): Database {
  const database = databases.find((candidate) => candidate.service_id === serviceID && candidate.name === name);
  if (!database) throw new ApiError('DATABASE_NOT_FOUND', 'Database not found.', 404);
  return database;
}

export const mockDatabaseApi = {
  async listServiceDatabases(serviceID: string): Promise<{ databases: Database[] }> {
    await wait();
    return { databases: clone(serviceDatabases(serviceID)) };
  },

  async createServiceDatabase(serviceID: string, body: CreateDatabaseRequest): Promise<Database> {
    await wait();
    if (!body.name.trim()) throw new ApiError('INVALID_REQUEST', 'Database name is required.', 400);
    if (databases.some((database) => database.service_id === serviceID && database.name === body.name)) {
      throw new ApiError('DATABASE_CONFLICT', 'Database already exists.', 409);
    }
    const database: Database = {
      name: body.name,
      service_id: serviceID,
      engine: engineFor(serviceID),
      size_bytes: 0,
      table_count: 0,
      created_at: now(),
      linked_sites: [],
      managed: true,
    };
    databases.push(database);
    return clone(database);
  },

  async getServiceDatabase(serviceID: string, name: string): Promise<Database> {
    await wait();
    return clone(findDatabase(serviceID, name));
  },

  async renameServiceDatabase(serviceID: string, name: string, body: RenameDatabaseRequest): Promise<Database> {
    await wait();
    if (engineFor(serviceID) === 'mysql' || engineFor(serviceID) === 'mariadb') {
      throw new ApiError('DATABASE_UNSUPPORTED', 'MySQL/MariaDB does not support database rename.', 400);
    }
    const database = findDatabase(serviceID, name);
    database.name = body.name;
    return clone(database);
  },

  async dropServiceDatabase(serviceID: string, name: string, body: DropDatabaseRequest): Promise<void> {
    await wait();
    const database = findDatabase(serviceID, name);
    if (database.name === 'devstack') throw new ApiError('DATABASE_PROTECTED', 'The devstack database is protected.', 409);
    if (body.confirm !== name) throw new ApiError('DATABASE_CONFLICT', 'Confirmation does not match.', 409);
    databases = databases.filter((candidate) => candidate !== database);
  },

  async listServiceUsers(serviceID: string): Promise<{ users: DatabaseUser[] }> {
    await wait();
    return { users: clone(serviceUsers(serviceID)) };
  },

  async createServiceUser(serviceID: string, body: CreateDatabaseUserRequest): Promise<DatabaseUser> {
    await wait();
    if (!body.username.trim()) throw new ApiError('INVALID_REQUEST', 'Username is required.', 400);
    if (users.some((user) => user.service_id === serviceID && user.username === body.username)) {
      throw new ApiError('DATABASE_CONFLICT', 'User already exists.', 409);
    }
    const user: DatabaseUser = {
      id: `usr_${body.username}`,
      service_id: serviceID,
      engine: engineFor(serviceID),
      username: body.username,
      host: body.host,
      enabled: true,
      managed: true,
      password_managed: true,
      access_level: body.access_level,
      grants: body.grants ?? [],
      linked_sites: [],
      created_at: now(),
    };
    users.push(user);
    return clone(user);
  },

  async updateServiceUser(serviceID: string, userID: string, body: UpdateDatabaseUserRequest): Promise<DatabaseUser> {
    await wait();
    const user = findUser(serviceID, userID);
    if (body.enabled !== undefined) user.enabled = body.enabled;
    if (body.grants) user.grants = body.grants;
    if (body.access_level) user.access_level = body.access_level;
    return clone(user);
  },

  async deleteServiceUser(serviceID: string, userID: string, body: DeleteDatabaseUserRequest): Promise<void> {
    await wait();
    const user = findUser(serviceID, userID);
    if (user.username === 'default' || user.username === 'devstack') {
      throw new ApiError('DATABASE_PROTECTED', 'This user is protected.', 409);
    }
    if (body.confirm !== user.username) throw new ApiError('DATABASE_CONFLICT', 'Confirmation does not match.', 409);
    if (links.some((link) => link.user_id === userID)) {
      throw new ApiError('DATABASE_IN_USE', 'Unlink the user from its sites first.', 409);
    }
    users = users.filter((candidate) => candidate !== user);
  },

  async setServiceUserPassword(serviceID: string, userID: string): Promise<DatabaseSecret> {
    await wait();
    const user = findUser(serviceID, userID);
    user.password_managed = true;
    return { value: 'mock_db_password' };
  },

  async revealServiceUserPassword(serviceID: string, userID: string): Promise<DatabaseSecret> {
    await wait();
    const user = findUser(serviceID, userID);
    if (!user.password_managed) throw new ApiError('DATABASE_PASSWORD_MISSING', 'Password is not managed.', 404);
    return { value: 'mock_db_password' };
  },

  async listServiceBackups(serviceID: string): Promise<{ backups: DatabaseBackup[] }> {
    await wait();
    return { backups: clone(backups.filter((backup) => backup.service_id === serviceID)) };
  },

  async createServiceBackup(serviceID: string, database: string, body: CreateDatabaseBackupRequest): Promise<DatabaseJob> {
    await wait();
    findDatabase(serviceID, database);
    const backup: DatabaseBackup = {
      id: `bkp_${backups.length + 1}`,
      service_id: serviceID,
      engine: engineFor(serviceID),
      database,
      size_bytes: 1024,
      sha256: 'mock',
      kind: body.kind ?? 'manual',
      status: 'ready',
      created_at: now(),
    };
    backups.push(backup);
    const job: DatabaseJob = {
      id: `job_${jobs.length + 1}`,
      service_id: serviceID,
      database,
      kind: 'backup',
      status: 'succeeded',
      progress: 100,
      backup_id: backup.id,
      created_at: now(),
      updated_at: now(),
    };
    jobs.push(job);
    return clone(job);
  },

  async deleteServiceBackup(serviceID: string, backupID: string): Promise<void> {
    await wait();
    backups = backups.filter((backup) => !(backup.service_id === serviceID && backup.id === backupID));
  },

  async restoreServiceBackup(serviceID: string, backupID: string, body: RestoreDatabaseBackupRequest): Promise<DatabaseJob> {
    await wait();
    const backup = backups.find((candidate) => candidate.service_id === serviceID && candidate.id === backupID);
    if (!backup) throw new ApiError('DATABASE_NOT_FOUND', 'Backup not found.', 404);
    if (body.overwrite && body.confirm !== (body.target ?? backup.database)) {
      throw new ApiError('DATABASE_CONFLICT', 'Confirmation does not match.', 409);
    }
    const job: DatabaseJob = {
      id: `job_${jobs.length + 1}`,
      service_id: serviceID,
      database: body.target ?? `${backup.database}_restore`,
      kind: 'restore',
      status: 'succeeded',
      progress: 100,
      backup_id: backupID,
      created_at: now(),
      updated_at: now(),
    };
    jobs.push(job);
    return clone(job);
  },

  async downloadServiceBackup(_serviceID: string, _backupID: string): Promise<Blob> {
    await wait();
    return new Blob(['mock backup'], { type: 'application/gzip' });
  },

  async importServiceDatabase(serviceID: string, database: string, _file: File, body: ImportDatabaseRequest): Promise<DatabaseJob> {
    await wait();
    findDatabase(serviceID, database);
    const job: DatabaseJob = {
      id: `job_${jobs.length + 1}`,
      service_id: serviceID,
      database,
      kind: 'import',
      status: 'succeeded',
      progress: 100,
      message: body.mode === 'replace' ? 'replaced' : 'merged',
      created_at: now(),
      updated_at: now(),
    };
    jobs.push(job);
    return clone(job);
  },

  async exportServiceDatabase(_serviceID: string, _database: string): Promise<Blob> {
    await wait();
    return new Blob(['mock export'], { type: 'application/gzip' });
  },

  async getDatabaseJob(jobID: string): Promise<DatabaseJob> {
    await wait();
    const job = jobs.find((candidate) => candidate.id === jobID);
    if (!job) throw new ApiError('DATABASE_JOB_NOT_FOUND', 'Job not found.', 404);
    return clone(job);
  },

  async cancelDatabaseJob(jobID: string): Promise<void> {
    await wait();
    const job = jobs.find((candidate) => candidate.id === jobID);
    if (job) job.status = 'canceled';
  },

  async listSiteDatabases(siteID: string): Promise<{ links: DatabaseLink[] }> {
    await wait();
    return { links: clone(links.filter((link) => link.site_id === siteID)) };
  },

  async linkSiteDatabase(siteID: string, body: LinkDatabaseRequest): Promise<DatabaseLink> {
    await wait();
    const database = findDatabase(body.service_id, body.database);
    const user = findUser(body.service_id, body.user_id);
    if (
      links.some(
        (candidate) =>
          candidate.site_id === siteID &&
          candidate.service_id === body.service_id &&
          candidate.database === database.name,
      )
    ) {
      throw new ApiError(
        'DATABASE_LINK_EXISTS',
        'This database is already linked to the site.',
        409,
      );
    }
    const primary = body.primary ?? true;
    if (primary && links.some((candidate) => candidate.site_id === siteID && candidate.primary)) {
      throw new ApiError(
        'DATABASE_PRIMARY_EXISTS',
        'The site already has a primary database link.',
        409,
      );
    }
    const link: DatabaseLink = {
      id: `lnk_${links.length + 1}`,
      site_id: siteID,
      service_id: body.service_id,
      engine: database.engine,
      database: database.name,
      user_id: user.id,
      username: user.username,
      host: user.host,
      primary,
      password_managed: user.password_managed,
      available: true,
      connection: connectionFor({ engine: database.engine, database: database.name, username: user.username, password_managed: user.password_managed }),
    };
    links.push(link);
    const siteRef = { id: siteID, name: siteID };
    if (!Array.isArray(database.linked_sites)) database.linked_sites = [];
    if (!database.linked_sites.some((site) => site.id === siteID)) {
      database.linked_sites.push({ ...siteRef });
    }
    if (!Array.isArray(user.linked_sites)) user.linked_sites = [];
    if (!user.linked_sites.some((site) => site.id === siteID)) {
      user.linked_sites.push({ ...siteRef });
    }
    return clone(link);
  },

  async unlinkSiteDatabase(siteID: string, linkID: string): Promise<void> {
    await wait();
    const link = links.find(
      (candidate) => candidate.site_id === siteID && candidate.id === linkID,
    );
    links = links.filter((link) => !(link.site_id === siteID && link.id === linkID));
    if (!link) return;
    if (
      !links.some(
        (candidate) =>
          candidate.site_id === siteID &&
          candidate.service_id === link.service_id &&
          candidate.database === link.database,
      )
    ) {
      const database = databases.find(
        (candidate) =>
          candidate.service_id === link.service_id && candidate.name === link.database,
      );
      if (database?.linked_sites) {
        database.linked_sites = database.linked_sites.filter(
          (site) => site.id !== siteID,
        );
      }
    }
    if (
      !links.some(
        (candidate) =>
          candidate.site_id === siteID &&
          candidate.service_id === link.service_id &&
          candidate.user_id === link.user_id,
      )
    ) {
      const user = users.find(
        (candidate) =>
          candidate.service_id === link.service_id && candidate.id === link.user_id,
      );
      if (user?.linked_sites) {
        user.linked_sites = user.linked_sites.filter((site) => site.id !== siteID);
      }
    }
  },

  async createSiteDatabase(siteID: string, body: CreateSiteDatabaseRequest): Promise<SiteDatabaseResult> {
    await wait();
    const database = body.new_database
      ? await this.createServiceDatabase(body.service_id, body.new_database)
      : findDatabase(body.service_id, body.database ?? '');
    const user = body.new_user
      ? await this.createServiceUser(body.service_id, body.new_user)
      : findUser(body.service_id, body.user_id ?? '');
    const link = await mockDatabaseApi.linkSiteDatabase(siteID, {
      service_id: body.service_id,
      database: database.name,
      user_id: user.id,
      primary: body.primary ?? true,
    });
    return { link, database, user };
  },
};
