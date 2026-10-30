export type DatabaseEngine = 'mysql' | 'mariadb' | 'postgres' | 'redis';

export type GrantPreset =
  | 'read_only'
  | 'read_write'
  | 'read_write_schema'
  | 'admin'
  | 'custom';

export type DatabaseJobStatus =
  | 'queued'
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'canceled';

export type DatabaseBackupKind =
  | 'manual'
  | 'pre_drop'
  | 'pre_restore'
  | 'pre_import';

export interface DatabaseSiteRef {
  id: string;
  name: string;
}

export interface Database {
  name: string;
  service_id: string;
  engine: DatabaseEngine;
  size_bytes: number;
  table_count: number;
  created_at?: string;
  linked_sites?: DatabaseSiteRef[];
  managed: boolean;
  missing?: boolean;
}

export interface DatabaseGrant {
  database: string;
  preset: GrantPreset;
  privileges?: string[];
}

export interface DatabaseUser {
  id: string;
  service_id: string;
  engine: DatabaseEngine;
  username: string;
  host?: string;
  enabled: boolean;
  managed: boolean;
  password_managed: boolean;
  access_level?: 'read-only' | 'full' | string;
  grants?: DatabaseGrant[];
  linked_sites?: DatabaseSiteRef[];
  created_at?: string;
  last_password_rotated_at?: string;
}

export interface DatabaseConnection {
  engine: DatabaseEngine;
  host: string;
  port: number;
  database: string;
  username: string;
  password_managed: boolean;
  url?: string;
  env?: Record<string, string>;
}

export interface DatabaseLink {
  id: string;
  site_id: string;
  site_name?: string;
  service_id: string;
  engine: DatabaseEngine;
  database: string;
  user_id: string;
  username: string;
  host?: string;
  primary: boolean;
  password_managed: boolean;
  available: boolean;
  error?: string;
  connection: DatabaseConnection;
}

export interface DatabaseBackup {
  id: string;
  service_id: string;
  engine: DatabaseEngine;
  database: string;
  size_bytes: number;
  sha256: string;
  kind: DatabaseBackupKind;
  status: 'ready' | 'running' | 'failed';
  created_at: string;
  error?: string;
}

export interface DatabaseJob {
  id: string;
  service_id: string;
  database: string;
  kind: 'backup' | 'restore' | 'import';
  status: DatabaseJobStatus;
  progress: number;
  message?: string;
  backup_id?: string;
  created_at: string;
  updated_at: string;
}

export interface DatabaseSecret {
  value: string;
}

export interface CreateDatabaseRequest {
  name: string;
  charset?: string;
  collation?: string;
}

export interface RenameDatabaseRequest {
  name: string;
}

export interface DropDatabaseRequest {
  confirm: string;
  backup_first?: boolean;
}

export interface CreateDatabaseUserRequest {
  username: string;
  host?: string;
  grants?: DatabaseGrant[];
  access_level?: string;
}

export interface UpdateDatabaseUserRequest {
  enabled?: boolean;
  grants?: DatabaseGrant[];
  access_level?: string;
}

export interface DeleteDatabaseUserRequest {
  confirm: string;
  reassign_owned?: boolean;
}

export interface CreateDatabaseBackupRequest {
  kind?: DatabaseBackupKind;
}

export interface RestoreDatabaseBackupRequest {
  target?: string;
  overwrite?: boolean;
  confirm?: string;
  pre_restore_backup?: boolean;
}

export interface ImportDatabaseRequest {
  mode?: 'merge' | 'replace';
  confirm?: string;
  pre_import_backup?: boolean;
}

export interface LinkDatabaseRequest {
  service_id: string;
  database: string;
  user_id: string;
  password?: string;
  primary?: boolean;
}

export interface CreateSiteDatabaseRequest {
  service_id: string;
  database?: string;
  new_database?: CreateDatabaseRequest;
  user_id?: string;
  new_user?: CreateDatabaseUserRequest;
  password?: string;
  grant?: GrantPreset;
  primary?: boolean;
}

export interface SiteDatabaseResult {
  link: DatabaseLink;
  database: Database;
  user: DatabaseUser;
  warning?: string;
}

export type SiteDatabaseLink = DatabaseLink;
