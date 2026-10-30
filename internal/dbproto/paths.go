package dbproto

import "net/url"

func serviceBase(id string) string {
	return "/v1/services/" + url.PathEscape(id)
}

func ServiceDatabases(id string) string {
	return serviceBase(id) + "/databases"
}

func ServiceDatabase(id, name string) string {
	return ServiceDatabases(id) + "/" + url.PathEscape(name)
}

func ServiceUsers(id string) string {
	return serviceBase(id) + "/users"
}

func ServiceUser(id, userID string) string {
	return ServiceUsers(id) + "/" + url.PathEscape(userID)
}

func ServiceUserPassword(id, userID string) string {
	return ServiceUser(id, userID) + "/password"
}

func ServiceUserReveal(id, userID string) string {
	return ServiceUser(id, userID) + "/reveal"
}

func ServiceDatabaseBackups(id, name string) string {
	return ServiceDatabase(id, name) + "/backups"
}

func ServiceBackup(id, backupID string) string {
	return serviceBase(id) + "/backups/" + url.PathEscape(backupID)
}

func ServiceBackupDownload(id, backupID string) string {
	return ServiceBackup(id, backupID) + "/download"
}

func ServiceBackupRestore(id, backupID string) string {
	return ServiceBackup(id, backupID) + "/restore"
}

func ServiceDatabaseImport(id, name string) string {
	return ServiceDatabase(id, name) + "/import"
}

func ServiceDatabaseExport(id, name string) string {
	return ServiceDatabase(id, name) + "/export"
}

func ServiceDatabaseEnv(id string) string {
	return serviceBase(id) + "/database-env"
}

func ServiceJob(id, jobID string) string {
	return serviceBase(id) + "/jobs/" + url.PathEscape(jobID)
}
