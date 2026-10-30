package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

const maxDatabaseImportBytes = 1 << 30

func (a *App) newDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Manage service databases, users, and backups",
	}
	cmd.AddCommand(
		a.newDBListCmd(),
		a.newDBCreateCmd(),
		a.newDBRenameCmd(),
		a.newDBDropCmd(),
		a.newDBUserCmd(),
		a.newDBBackupCmd(),
		a.newDBExportCmd(),
		a.newDBImportCmd(),
		a.newDBLinkCmd(),
		a.newDBUnlinkCmd(),
		a.newDBPasswordCmd(),
	)
	return cmd
}

func (a *App) newDBListCmd() *cobra.Command {
	var service string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List databases on a service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			if service == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service is required", Remediation: "Pass the service id, for example `--service mysql`."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			list, err := c.ListServiceDatabases(ctx, service)
			if err != nil {
				return err
			}
			if list == nil {
				list = []model.Database{}
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					Databases []model.Database `json:"databases"`
				}{Databases: list})
			}
			rows := make([][]string, 0, len(list))
			for _, database := range list {
				rows = append(rows, []string{
					database.Name,
					defaultString(string(database.Engine), "-"),
					formatBytes(database.SizeBytes),
					strconv.Itoa(database.TableCount),
					formatDatabaseSites(database.LinkedSites),
					strconv.FormatBool(database.Managed),
					strconv.FormatBool(database.Missing),
				})
			}
			return writeTable(cmd, []string{"NAME", "ENGINE", "SIZE", "TABLES", "LINKED", "MANAGED", "MISSING"}, rows)
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id (mysql, mariadb, postgres, or redis)")
	return cmd
}

func (a *App) newDBCreateCmd() *cobra.Command {
	var service, name, charset, collation string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a database on a service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			name = strings.TrimSpace(name)
			if service == "" || name == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service and --name are required", Remediation: "For example `devstack db create --service mysql --name blog`."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			database, err := c.CreateServiceDatabase(ctx, service, databases.CreateDatabaseRequest{
				Name: name, Charset: strings.TrimSpace(charset), Collation: strings.TrimSpace(collation),
			})
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, database)
			}
			return writeDatabaseDetails(cmd, database)
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id")
	cmd.Flags().StringVar(&name, "name", "", "database name")
	cmd.Flags().StringVar(&charset, "charset", "", "optional database character set")
	cmd.Flags().StringVar(&collation, "collation", "", "optional database collation")
	return cmd
}

func (a *App) newDBRenameCmd() *cobra.Command {
	var service, name, to string
	cmd := &cobra.Command{
		Use:   "rename",
		Short: "Rename a PostgreSQL database",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			name = strings.TrimSpace(name)
			to = strings.TrimSpace(to)
			if service == "" || name == "" || to == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service, --name, and --to are required", Remediation: "For example `devstack db rename --service postgres --name app --to app2`."}
			}
			switch strings.ToLower(service) {
			case "mysql", "mariadb":
				return &Error{
					Code:        "DATABASE_UNSUPPORTED",
					Message:     fmt.Sprintf("the %s engine does not support database rename; back up, create a new database, and import", service),
					Remediation: "Use `devstack db backup create`, `devstack db create`, and `devstack db import` instead.",
				}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			database, err := c.RenameServiceDatabase(ctx, service, name, databases.RenameDatabaseRequest{Name: to})
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, database)
			}
			return writeDatabaseDetails(cmd, database)
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id (PostgreSQL only)")
	cmd.Flags().StringVar(&name, "name", "", "current database name")
	cmd.Flags().StringVar(&to, "to", "", "new database name")
	return cmd
}

func (a *App) newDBDropCmd() *cobra.Command {
	var service, name string
	var backupFirst bool
	cmd := &cobra.Command{
		Use:   "drop",
		Short: "Drop a database after confirmation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			name = strings.TrimSpace(name)
			if service == "" || name == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service and --name are required", Remediation: "For example `devstack db drop --service mysql --name blog --yes`."}
			}
			if err := a.requireDestructiveConfirmation(cmd,
				fmt.Sprintf("Drop database %q on service %q? This cannot be undone.", name, service),
				"Re-run with --yes to confirm the drop."); err != nil {
				return err
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			if err := c.DropServiceDatabase(ctx, service, name, databases.DropDatabaseRequest{Confirm: name, BackupFirst: backupFirst}); err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					ServiceID string `json:"service_id"`
					Database  string `json:"database"`
					Dropped   bool   `json:"dropped"`
				}{ServiceID: service, Database: name, Dropped: true})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Dropped database %s on %s.\n", name, service)
			return err
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id")
	cmd.Flags().StringVar(&name, "name", "", "database name")
	cmd.Flags().BoolVar(&backupFirst, "backup-first", false, "create a pre-drop backup before dropping")
	return cmd
}

func (a *App) newDBUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage engine users",
	}
	cmd.AddCommand(
		a.newDBUserListCmd(),
		a.newDBUserCreateCmd(),
		a.newDBUserDeleteCmd(),
		a.newDBUserPasswordCmd(),
		a.newDBUserGrantCmd(),
	)
	return cmd
}

func (a *App) newDBUserListCmd() *cobra.Command {
	var service string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List users on a service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			if service == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service is required", Remediation: "Pass the service id, for example `--service mysql`."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			users, err := c.ListServiceUsers(ctx, service)
			if err != nil {
				return err
			}
			if users == nil {
				users = []model.DatabaseUser{}
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					Users []model.DatabaseUser `json:"users"`
				}{Users: users})
			}
			rows := make([][]string, 0, len(users))
			for _, user := range users {
				rows = append(rows, []string{
					user.ID,
					user.Username,
					defaultString(user.Host, "-"),
					strconv.FormatBool(user.Enabled),
					databasePasswordState(user.PasswordManaged),
					defaultString(user.AccessLevel, "-"),
					formatDatabaseGrants(user.Grants),
					formatDatabaseSites(user.LinkedSites),
				})
			}
			return writeTable(cmd, []string{"ID", "USERNAME", "HOST", "ENABLED", "PASSWORD", "ACCESS", "GRANTS", "LINKED"}, rows)
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id")
	return cmd
}

func (a *App) newDBUserCreateCmd() *cobra.Command {
	var service, name, host, accessLevel string
	var grants, privileges []string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an engine user",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			name = strings.TrimSpace(name)
			if service == "" || name == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service and --name are required", Remediation: "For example `devstack db user create --service mysql --name blog_user --grant blog:read_write_schema`."}
			}
			parsed, err := parseDatabaseGrants(grants, privileges)
			if err != nil {
				return err
			}
			accessLevel = strings.ToLower(strings.TrimSpace(accessLevel))
			if accessLevel != "" && !validRedisAccessLevel(accessLevel) {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--access-level must be read-only or full", Remediation: "Use `--access-level read-only` or `--access-level full`."}
			}
			if accessLevel == "read_only" {
				accessLevel = "read-only"
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			user, err := c.CreateServiceUser(ctx, service, databases.CreateUserRequest{
				Username: name, Host: strings.TrimSpace(host), Grants: parsed, AccessLevel: accessLevel,
			})
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, user)
			}
			if err := writeDatabaseUserDetails(cmd, user); err != nil {
				return err
			}
			if user.PasswordManaged {
				fmt.Fprintf(cmd.OutOrStdout(), "Password: generated; run `devstack db user password --service %s --user-id %s --reveal` to view it once.\n", service, user.ID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id")
	cmd.Flags().StringVar(&name, "name", "", "username")
	cmd.Flags().StringVar(&host, "host", "", "MySQL/MariaDB host scope (defaults to %)")
	cmd.Flags().StringArrayVar(&grants, "grant", nil, "database-scoped grant as <database>:<preset> (repeatable)")
	cmd.Flags().StringArrayVar(&privileges, "privilege", nil, "custom privilege for a custom grant (repeatable)")
	cmd.Flags().StringVar(&accessLevel, "access-level", "", "Redis access level: read-only or full")
	return cmd
}

func (a *App) newDBUserDeleteCmd() *cobra.Command {
	var service, userID string
	var reassignOwned bool
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete an engine user after confirmation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			userID = strings.TrimSpace(userID)
			if service == "" || userID == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service and --user-id are required", Remediation: "For example `devstack db user delete --service mysql --user-id usr_blog --yes`."}
			}
			if err := a.requireDestructiveConfirmation(cmd,
				fmt.Sprintf("Delete user %q on service %q? Databases and data are not dropped.", userID, service),
				"Re-run with --yes to confirm the user deletion."); err != nil {
				return err
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			user, err := c.GetServiceUser(ctx, service, userID)
			if err != nil {
				return err
			}
			confirm := defaultString(user.Username, userID)
			if err := c.DeleteServiceUser(ctx, service, userID, databases.DeleteUserRequest{Confirm: confirm, ReassignOwned: reassignOwned}); err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					ServiceID string `json:"service_id"`
					UserID    string `json:"user_id"`
					Deleted   bool   `json:"deleted"`
				}{ServiceID: service, UserID: userID, Deleted: true})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted user %s on %s.\n", confirm, service)
			return err
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id")
	cmd.Flags().StringVar(&userID, "user-id", "", "opaque database user id")
	cmd.Flags().BoolVar(&reassignOwned, "reassign-owned", false, "PostgreSQL: reassign owned objects to the devstack admin before dropping the role")
	return cmd
}

func (a *App) newDBUserPasswordCmd() *cobra.Command {
	var service, userID string
	var reveal, set, rotate bool
	cmd := &cobra.Command{
		Use:   "password",
		Short: "Reveal or rotate an engine user password",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			userID = strings.TrimSpace(userID)
			if service == "" || userID == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service and --user-id are required", Remediation: "For example `devstack db user password --service mysql --user-id usr_blog --reveal`."}
			}
			if set {
				return &Error{
					Code:    "DATABASE_PASSWORD_SET_UNSUPPORTED",
					Message: "setting a user-supplied database password is not supported by the current API contract",
					Remediation: "Omit --set to rotate a devstack-generated password (or pass --rotate), " +
						"or set an external user's password at link time with `site db link --password-stdin` " +
						"or `site add --database-user-password-stdin`.",
				}
			}
			if reveal && rotate {
				return &Error{Code: "INVALID_FLAGS", Message: "--reveal and --rotate cannot be used together", Remediation: "Choose one action."}
			}
			if !reveal {
				if err := a.requireDestructiveConfirmation(cmd,
					fmt.Sprintf("Rotate the password for user %q on service %q? Existing connections keep working until they reconnect.", userID, service),
					"Re-run with --yes to confirm the password rotation."); err != nil {
					return err
				}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			if reveal {
				secret, err := c.RevealServiceUserPassword(ctx, service, userID)
				if err != nil {
					return err
				}
				a.warnSecret(cmd, "WARNING: this password is a secret; do not share or log it.")
				if a.jsonOutput {
					return a.writeJSON(cmd, secret)
				}
				return writeSecretTable(cmd, "Password", secret.Value)
			}
			secret, err := c.SetServiceUserPassword(ctx, service, userID)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					UserID          string `json:"user_id"`
					Rotated         bool   `json:"rotated"`
					PasswordManaged bool   `json:"password_managed"`
				}{UserID: userID, Rotated: true, PasswordManaged: secret.Value != ""})
			}
			a.warnSecret(cmd, "WARNING: the new password is shown once; do not share or log it.")
			return writeSecretTable(cmd, "New password", secret.Value)
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id")
	cmd.Flags().StringVar(&userID, "user-id", "", "opaque database user id")
	cmd.Flags().BoolVar(&reveal, "reveal", false, "reveal the stored password once")
	cmd.Flags().BoolVar(&rotate, "rotate", false, "rotate to a devstack-generated password (default when neither --reveal nor --set is used)")
	cmd.Flags().BoolVar(&set, "set", false, "unsupported: setting a user-supplied password needs a future API contract; fails without an API call")
	return cmd
}

func (a *App) newDBUserGrantCmd() *cobra.Command {
	var service, userID, database, preset string
	var privileges []string
	cmd := &cobra.Command{
		Use:   "grant",
		Short: "Set a database-scoped grant for an engine user",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			userID = strings.TrimSpace(userID)
			database = strings.TrimSpace(database)
			preset = strings.TrimSpace(preset)
			if service == "" || userID == "" || database == "" || preset == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service, --user-id, --database, and --preset are required", Remediation: "For example `devstack db user grant --service mysql --user-id usr_blog --database blog --preset read_write_schema`."}
			}
			grant, err := databaseGrant(database, preset, privileges)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			user, err := c.GetServiceUser(ctx, service, userID)
			if err != nil {
				return err
			}
			grants := mergeDatabaseGrant(user.Grants, grant)
			updated, err := c.UpdateServiceUser(ctx, service, userID, databases.UpdateUserRequest{Grants: grants})
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, updated)
			}
			return writeDatabaseUserDetails(cmd, updated)
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id")
	cmd.Flags().StringVar(&userID, "user-id", "", "opaque database user id")
	cmd.Flags().StringVar(&database, "database", "", "database name")
	cmd.Flags().StringVar(&preset, "preset", "", "grant preset: read_only, read_write, read_write_schema, admin, or custom")
	cmd.Flags().StringArrayVar(&privileges, "privilege", nil, "custom privilege for --preset custom (repeatable)")
	return cmd
}

func (a *App) newDBBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Manage database backups",
	}
	cmd.AddCommand(
		a.newDBBackupListCmd(),
		a.newDBBackupCreateCmd(),
		a.newDBBackupDownloadCmd(),
		a.newDBBackupRestoreCmd(),
		a.newDBBackupDeleteCmd(),
	)
	return cmd
}

func (a *App) newDBBackupListCmd() *cobra.Command {
	var service string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List database backups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			if service == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service is required", Remediation: "Pass the service id, for example `--service mysql`."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			backups, err := c.ListServiceBackups(ctx, service)
			if err != nil {
				return err
			}
			if backups == nil {
				backups = []model.DatabaseBackup{}
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					Backups []model.DatabaseBackup `json:"backups"`
				}{Backups: backups})
			}
			return writeBackupTable(cmd, backups)
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id")
	return cmd
}

func (a *App) newDBBackupCreateCmd() *cobra.Command {
	var service, database string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a database backup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			database = strings.TrimSpace(database)
			if service == "" || database == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service and --database are required", Remediation: "For example `devstack db backup create --service mysql --database blog`."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			job, err := c.CreateServiceBackup(ctx, service, database, databases.CreateBackupRequest{Kind: model.BackupManual})
			if err != nil {
				return err
			}
			return writeDatabaseJob(cmd, a, job)
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id")
	cmd.Flags().StringVar(&database, "database", "", "database name")
	return cmd
}

func (a *App) newDBBackupDownloadCmd() *cobra.Command {
	var service, backupID, output string
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download a database backup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			backupID = strings.TrimSpace(backupID)
			output = strings.TrimSpace(output)
			if service == "" || backupID == "" || output == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service, --backup-id, and --output are required", Remediation: "For example `devstack db backup download --service mysql --backup-id bkp_1 --output app.sql.gz`."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			body, err := c.DownloadServiceBackup(ctx, service, backupID)
			if err != nil {
				return err
			}
			defer func() { _ = body.Close() }()
			written, err := a.writeDownload(cmd, output, body)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					ServiceID string `json:"service_id"`
					BackupID  string `json:"backup_id"`
					Output    string `json:"output"`
					Bytes     int64  `json:"bytes"`
				}{ServiceID: service, BackupID: backupID, Output: output, Bytes: written})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Downloaded backup %s to %s (%s).\n", backupID, output, formatBytes(written))
			return err
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id")
	cmd.Flags().StringVar(&backupID, "backup-id", "", "backup id")
	cmd.Flags().StringVar(&output, "output", "", "destination file, or - for stdout")
	return cmd
}

func (a *App) newDBBackupRestoreCmd() *cobra.Command {
	var service, backupID, target string
	var overwrite bool
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a database backup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			backupID = strings.TrimSpace(backupID)
			target = strings.TrimSpace(target)
			if service == "" || backupID == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service and --backup-id are required", Remediation: "For example `devstack db backup restore --service mysql --backup-id bkp_1`."}
			}
			if overwrite && target == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--target is required with --overwrite", Remediation: "Pass the database to overwrite with `--target`."}
			}
			if overwrite {
				if err := a.requireDestructiveConfirmation(cmd,
					fmt.Sprintf("Overwrite database %q on service %q with backup %q? The current contents are replaced.", target, service, backupID),
					"Re-run with --yes to confirm the overwrite restore."); err != nil {
					return err
				}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			job, err := c.RestoreServiceBackup(ctx, service, backupID, databases.RestoreBackupRequest{
				Target: target, Overwrite: overwrite, Confirm: target, PreRestoreBackup: overwrite,
			})
			if err != nil {
				return err
			}
			return writeDatabaseJob(cmd, a, job)
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id")
	cmd.Flags().StringVar(&backupID, "backup-id", "", "backup id")
	cmd.Flags().StringVar(&target, "target", "", "target database (defaults to a new <database>_restore_<timestamp>)")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "drop and recreate the target database after a pre-restore backup")
	return cmd
}

func (a *App) newDBBackupDeleteCmd() *cobra.Command {
	var service, backupID string
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a database backup after confirmation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			backupID = strings.TrimSpace(backupID)
			if service == "" || backupID == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service and --backup-id are required", Remediation: "For example `devstack db backup delete --service mysql --backup-id bkp_1 --yes`."}
			}
			if err := a.requireDestructiveConfirmation(cmd,
				fmt.Sprintf("Delete backup %q on service %q? The backup file is removed.", backupID, service),
				"Re-run with --yes to confirm the backup deletion."); err != nil {
				return err
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			if err := c.DeleteServiceBackup(ctx, service, backupID); err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					ServiceID string `json:"service_id"`
					BackupID  string `json:"backup_id"`
					Deleted   bool   `json:"deleted"`
				}{ServiceID: service, BackupID: backupID, Deleted: true})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted backup %s on %s.\n", backupID, service)
			return err
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id")
	cmd.Flags().StringVar(&backupID, "backup-id", "", "backup id")
	return cmd
}

func (a *App) newDBExportCmd() *cobra.Command {
	var service, database, output string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export a database to a SQL dump",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			database = strings.TrimSpace(database)
			output = strings.TrimSpace(output)
			if service == "" || database == "" || output == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service, --database, and --output are required", Remediation: "For example `devstack db export --service mysql --database blog --output blog.sql`."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			body, err := c.ExportServiceDatabase(ctx, service, database)
			if err != nil {
				return err
			}
			defer func() { _ = body.Close() }()
			written, err := a.writeDownload(cmd, output, body)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					ServiceID string `json:"service_id"`
					Database  string `json:"database"`
					Output    string `json:"output"`
					Bytes     int64  `json:"bytes"`
				}{ServiceID: service, Database: database, Output: output, Bytes: written})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Exported %s to %s (%s).\n", database, output, formatBytes(written))
			return err
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id")
	cmd.Flags().StringVar(&database, "database", "", "database name")
	cmd.Flags().StringVar(&output, "output", "", "destination file, or - for stdout")
	return cmd
}

func (a *App) newDBImportCmd() *cobra.Command {
	var service, database, file string
	var replace, preImportBackup bool
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import a SQL dump into an existing database",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			database = strings.TrimSpace(database)
			file = strings.TrimSpace(file)
			if service == "" || database == "" || file == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service, --database, and --file are required", Remediation: "For example `devstack db import --service mysql --database blog --file blog.sql`."}
			}
			lowerFile := strings.ToLower(file)
			if !strings.HasSuffix(lowerFile, ".sql") && !strings.HasSuffix(lowerFile, ".sql.gz") {
				return &Error{Code: "INVALID_ARGUMENT", Message: "import file must be a .sql or .sql.gz dump", Remediation: "Pass a .sql or .sql.gz file with --file."}
			}
			info, err := os.Stat(file)
			if err != nil {
				return &Error{Code: "FILE_NOT_FOUND", Message: err.Error(), Remediation: "Pass an existing .sql or .sql.gz file with --file."}
			}
			if info.IsDir() {
				return &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("%s is a directory", file), Remediation: "Pass a .sql or .sql.gz file."}
			}
			if info.Size() > maxDatabaseImportBytes {
				return &Error{Code: "PAYLOAD_TOO_LARGE", Message: fmt.Sprintf("import file is %s; the limit is 1 GiB", formatBytes(info.Size())), Remediation: "Split the dump into smaller files."}
			}
			mode := databases.ImportMerge
			confirm := ""
			if replace {
				mode = databases.ImportReplace
				confirm = database
				preImportBackup = true
				if err := a.requireDestructiveConfirmation(cmd,
					fmt.Sprintf("Replace the contents of database %q on service %q with %s? A pre-import backup is created first.", database, service, filepath.Base(file)),
					"Re-run with --yes to confirm the replace import."); err != nil {
					return err
				}
			}
			handle, err := os.Open(file)
			if err != nil {
				return err
			}
			defer func() { _ = handle.Close() }()
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			job, err := c.ImportServiceDatabase(ctx, service, database, handle, filepath.Base(file), databases.ImportRequest{
				Mode: mode, Confirm: confirm, PreImportBackup: preImportBackup,
			})
			if err != nil {
				return err
			}
			return writeDatabaseJob(cmd, a, job)
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id")
	cmd.Flags().StringVar(&database, "database", "", "database name")
	cmd.Flags().StringVar(&file, "file", "", "SQL dump file (.sql or .sql.gz, up to 1 GiB)")
	cmd.Flags().BoolVar(&replace, "replace", false, "replace the database contents after a pre-import backup")
	cmd.Flags().BoolVar(&preImportBackup, "pre-import-backup", false, "create a pre-import backup for a merge import")
	return cmd
}

func (a *App) newDBLinkCmd() *cobra.Command {
	var site, service, database, userID string
	var primary, passwordStdin bool
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Link a database and user to a site",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			site = strings.TrimSpace(site)
			service = strings.TrimSpace(service)
			database = strings.TrimSpace(database)
			userID = strings.TrimSpace(userID)
			if site == "" || service == "" || database == "" || userID == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--site, --service, --database, and --user-id are required", Remediation: "For example `devstack db link --site blog --service mysql --database blog --user-id usr_blog`."}
			}
			if strings.EqualFold(service, "redis") {
				return &Error{Code: "DATABASE_UNSUPPORTED", Message: "Redis users are not site-linkable in v1", Remediation: "Use `devstack db user` to manage Redis ACL users."}
			}
			password := ""
			if passwordStdin {
				value, err := readPasswordLine(cmd)
				if err != nil {
					return err
				}
				password = value
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			link, err := c.LinkSiteDatabase(ctx, site, databases.LinkDatabaseRequest{
				ServiceID: service, Database: database, UserID: userID, Password: password, Primary: primary,
			})
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, sanitizeDatabaseLink(link))
			}
			return writeDatabaseLinkDetails(cmd, link)
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site name or id")
	cmd.Flags().StringVar(&service, "service", "", "service id")
	cmd.Flags().StringVar(&database, "database", "", "database name")
	cmd.Flags().StringVar(&userID, "user-id", "", "opaque database user id")
	cmd.Flags().BoolVar(&primary, "primary", false, "make this the site's primary database link")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read an external user's password from stdin (never argv)")
	return cmd
}

func (a *App) newDBUnlinkCmd() *cobra.Command {
	var site, linkID string
	cmd := &cobra.Command{
		Use:   "unlink",
		Short: "Remove a site database link",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			site = strings.TrimSpace(site)
			linkID = strings.TrimSpace(linkID)
			if site == "" || linkID == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--site and --link-id are required", Remediation: "For example `devstack db unlink --site blog --link-id lnk_1 --yes`."}
			}
			if err := a.requireDestructiveConfirmation(cmd,
				fmt.Sprintf("Unlink database link %q from site %q? The database, user, and data are not dropped.", linkID, site),
				"Re-run with --yes to confirm the unlink."); err != nil {
				return err
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			if err := c.UnlinkSiteDatabase(ctx, site, linkID); err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					Site     string `json:"site"`
					LinkID   string `json:"link_id"`
					Unlinked bool   `json:"unlinked"`
				}{Site: site, LinkID: linkID, Unlinked: true})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Unlinked database link %s from %s.\n", linkID, site)
			return err
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site name or id")
	cmd.Flags().StringVar(&linkID, "link-id", "", "database link id")
	return cmd
}

func (a *App) newDBPasswordCmd() *cobra.Command {
	var service, user, userID string
	var reveal bool
	cmd := &cobra.Command{
		Use:   "password",
		Short: "Set or reveal the Redis password",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service = strings.TrimSpace(service)
			user = strings.TrimSpace(user)
			userID = strings.TrimSpace(userID)
			if service == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service is required", Remediation: "For example `devstack db password --service redis --reveal`."}
			}
			if user == "" {
				user = "default"
			}
			if !reveal {
				if err := a.requireDestructiveConfirmation(cmd,
					fmt.Sprintf("Rotate the %s password on service %q? Existing clients must be updated.", user, service),
					"Re-run with --yes to confirm the password rotation."); err != nil {
					return err
				}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			if reveal {
				resolved, err := resolveDatabaseUserID(ctx, c, service, user, userID)
				if err != nil {
					return err
				}
				secret, err := c.RevealServiceUserPassword(ctx, service, resolved)
				if err != nil {
					return err
				}
				a.warnSecret(cmd, "WARNING: this password is a secret; do not share or log it.")
				if a.jsonOutput {
					return a.writeJSON(cmd, secret)
				}
				return writeSecretTable(cmd, "Password", secret.Value)
			}
			var secret databases.Secret
			if user == "default" && userID == "" {
				secret, err = c.SetServicePassword(ctx, service)
			} else {
				resolved, resolveErr := resolveDatabaseUserID(ctx, c, service, user, userID)
				if resolveErr != nil {
					return resolveErr
				}
				secret, err = c.SetServiceUserPassword(ctx, service, resolved)
			}
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					ServiceID       string `json:"service_id"`
					User            string `json:"user"`
					Rotated         bool   `json:"rotated"`
					PasswordManaged bool   `json:"password_managed"`
				}{ServiceID: service, User: user, Rotated: true, PasswordManaged: secret.Value != ""})
			}
			a.warnSecret(cmd, "WARNING: the new password is shown once; do not share or log it.")
			return writeSecretTable(cmd, "New password", secret.Value)
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service id (redis)")
	cmd.Flags().StringVar(&user, "user", "default", "username (defaults to the Redis default user)")
	cmd.Flags().StringVar(&userID, "user-id", "", "opaque user id (overrides --user)")
	cmd.Flags().BoolVar(&reveal, "reveal", false, "reveal the stored password once")
	return cmd
}

// requireDestructiveConfirmation gates a mutation behind --yes or an
// interactive yes. A declined prompt makes no API call.
func (a *App) requireDestructiveConfirmation(cmd *cobra.Command, message, remediation string) error {
	if a.assumeYes {
		return nil
	}
	ok, err := a.confirm(cmd, message)
	if err != nil {
		return err
	}
	if !ok {
		return &Error{Code: "CONFIRMATION_REQUIRED", Message: message, Remediation: remediation}
	}
	return nil
}

func (a *App) warnSecret(cmd *cobra.Command, message string) {
	out := cmd.OutOrStdout()
	if a.jsonOutput {
		out = cmd.ErrOrStderr()
	}
	fmt.Fprintln(out, message)
}

func (a *App) writeDownload(cmd *cobra.Command, output string, body io.Reader) (int64, error) {
	if output == "-" {
		if a.jsonOutput {
			return 0, &Error{Code: "INVALID_FLAGS", Message: "--output - cannot be combined with --json", Remediation: "Write to a file, or drop --json for raw output."}
		}
		return io.Copy(cmd.OutOrStdout(), body)
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	// Tighten an existing file too; OpenFile's mode only applies on creation.
	_ = file.Chmod(0o600)
	written, copyErr := io.Copy(file, body)
	closeErr := file.Close()
	if copyErr != nil {
		return written, copyErr
	}
	return written, closeErr
}

func readPasswordLine(cmd *cobra.Command) (string, error) {
	reader := bufio.NewReader(cmd.InOrStdin())
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func resolveDatabaseUserID(ctx context.Context, c Client, serviceID, username, userID string) (string, error) {
	if userID != "" {
		return userID, nil
	}
	users, err := c.ListServiceUsers(ctx, serviceID)
	if err != nil {
		return "", err
	}
	for _, user := range users {
		if user.Username == username && (user.Host == "" || user.Host == "%") {
			return user.ID, nil
		}
	}
	if username == "default" {
		return "default", nil
	}
	return "", &Error{
		Code:        "DATABASE_USER_NOT_FOUND",
		Message:     fmt.Sprintf("no user named %q on service %q", username, serviceID),
		Remediation: "Run `devstack db user list --service <id>` to find the user id, then pass --user-id.",
	}
}

func parseDatabaseGrants(values, privileges []string) ([]model.DatabaseGrant, error) {
	if len(values) == 0 {
		return nil, nil
	}
	grants := make([]model.DatabaseGrant, 0, len(values))
	for _, raw := range values {
		database, preset, ok := strings.Cut(raw, ":")
		if !ok || strings.TrimSpace(database) == "" || strings.TrimSpace(preset) == "" {
			return nil, &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("invalid --grant %q, want <database>:<preset>", raw), Remediation: "For example `--grant blog:read_write_schema`."}
		}
		grant, err := databaseGrant(strings.TrimSpace(database), strings.TrimSpace(preset), privileges)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	return grants, nil
}

func databaseGrant(database, preset string, privileges []string) (model.DatabaseGrant, error) {
	value := model.DatabaseGrantPreset(strings.ToLower(strings.TrimSpace(preset)))
	switch value {
	case model.GrantReadOnly, model.GrantReadWrite, model.GrantReadWriteSchema, model.GrantAdmin:
	case model.GrantCustom:
		if len(privileges) == 0 {
			return model.DatabaseGrant{}, &Error{Code: "INVALID_ARGUMENT", Message: "--preset custom requires at least one --privilege", Remediation: "Pass a whitelisted privilege, for example `--privilege SELECT`."}
		}
	default:
		return model.DatabaseGrant{}, &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("unsupported grant preset %q", preset), Remediation: "Use read_only, read_write, read_write_schema, admin, or custom."}
	}
	cleaned := make([]string, 0, len(privileges))
	for _, privilege := range privileges {
		privilege = strings.TrimSpace(privilege)
		if privilege == "" {
			continue
		}
		cleaned = append(cleaned, privilege)
	}
	if value == model.GrantCustom && len(cleaned) == 0 {
		return model.DatabaseGrant{}, &Error{Code: "INVALID_ARGUMENT", Message: "--preset custom requires at least one non-empty --privilege", Remediation: "Pass a whitelisted privilege, for example `--privilege SELECT`."}
	}
	return model.DatabaseGrant{Database: database, Preset: value, Privileges: cleaned}, nil
}

func mergeDatabaseGrant(grants []model.DatabaseGrant, grant model.DatabaseGrant) []model.DatabaseGrant {
	merged := make([]model.DatabaseGrant, 0, len(grants)+1)
	replaced := false
	for _, existing := range grants {
		if existing.Database == grant.Database {
			merged = append(merged, grant)
			replaced = true
			continue
		}
		merged = append(merged, existing)
	}
	if !replaced {
		merged = append(merged, grant)
	}
	return merged
}

func validRedisAccessLevel(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "read-only", "read_only", "full":
		return true
	default:
		return false
	}
}

func formatDatabaseSites(sites []model.DatabaseSiteRef) string {
	if len(sites) == 0 {
		return "-"
	}
	names := make([]string, 0, len(sites))
	for _, site := range sites {
		names = append(names, defaultString(site.Name, site.ID))
	}
	return strings.Join(names, ",")
}

func formatDatabaseGrants(grants []model.DatabaseGrant) string {
	if len(grants) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(grants))
	for _, grant := range grants {
		parts = append(parts, grant.Database+":"+string(grant.Preset))
	}
	return strings.Join(parts, ",")
}

func databasePasswordState(managed bool) string {
	if managed {
		return "managed"
	}
	return "not managed"
}

func formatDatabaseTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func shortDatabaseSHA(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return defaultString(value, "-")
}

func writeDatabaseDetails(cmd *cobra.Command, database model.Database) error {
	return writeTable(cmd, []string{"FIELD", "VALUE"}, [][]string{
		{"Name", database.Name},
		{"Service", database.ServiceID},
		{"Engine", defaultString(string(database.Engine), "-")},
		{"Size", formatBytes(database.SizeBytes)},
		{"Tables", strconv.Itoa(database.TableCount)},
		{"Linked sites", formatDatabaseSites(database.LinkedSites)},
		{"Managed", strconv.FormatBool(database.Managed)},
		{"Missing", strconv.FormatBool(database.Missing)},
		{"Created", formatDatabaseTime(database.CreatedAt)},
	})
}

func writeDatabaseUserDetails(cmd *cobra.Command, user model.DatabaseUser) error {
	return writeTable(cmd, []string{"FIELD", "VALUE"}, [][]string{
		{"ID", user.ID},
		{"Service", user.ServiceID},
		{"Engine", defaultString(string(user.Engine), "-")},
		{"Username", user.Username},
		{"Host", defaultString(user.Host, "-")},
		{"Enabled", strconv.FormatBool(user.Enabled)},
		{"Managed", strconv.FormatBool(user.Managed)},
		{"Password", databasePasswordState(user.PasswordManaged)},
		{"Access level", defaultString(user.AccessLevel, "-")},
		{"Grants", formatDatabaseGrants(user.Grants)},
		{"Linked sites", formatDatabaseSites(user.LinkedSites)},
		{"Created", formatDatabaseTime(user.CreatedAt)},
	})
}

func writeBackupTable(cmd *cobra.Command, backups []model.DatabaseBackup) error {
	rows := make([][]string, 0, len(backups))
	for _, backup := range backups {
		rows = append(rows, []string{
			backup.ID,
			backup.Database,
			defaultString(string(backup.Kind), "-"),
			defaultString(string(backup.Status), "-"),
			formatBytes(backup.SizeBytes),
			formatDatabaseTime(backup.CreatedAt),
			shortDatabaseSHA(backup.SHA256),
		})
	}
	return writeTable(cmd, []string{"ID", "DATABASE", "KIND", "STATUS", "SIZE", "CREATED", "SHA256"}, rows)
}

func writeDatabaseJob(cmd *cobra.Command, app *App, job model.DatabaseJob) error {
	if app.jsonOutput {
		return app.writeJSON(cmd, job)
	}
	return writeTable(cmd, []string{"FIELD", "VALUE"}, [][]string{
		{"Job", job.ID},
		{"Service", job.ServiceID},
		{"Database", job.Database},
		{"Kind", defaultString(string(job.Kind), "-")},
		{"Status", defaultString(string(job.Status), "-")},
		{"Progress", strconv.Itoa(job.Progress)},
		{"Message", defaultString(job.Message, "-")},
		{"Backup", defaultString(job.BackupID, "-")},
	})
}

// sanitizeDatabaseLink is defense-in-depth for CLI JSON output: list/detail
// responses must never expose a password, even if a platform adapter includes
// one in a connection URL or env snippet.
func sanitizeDatabaseLink(link model.DatabaseLink) model.DatabaseLink {
	link.Connection.URL = redactDatabaseURL(link.Connection.URL)
	if len(link.Connection.Env) == 0 {
		return link
	}
	env := make(map[string]string, len(link.Connection.Env))
	for key, value := range link.Connection.Env {
		switch {
		case strings.EqualFold(key, "DB_PASSWORD"):
			continue
		case strings.EqualFold(key, "DATABASE_URL"):
			value = redactDatabaseURL(value)
		}
		env[key] = value
	}
	link.Connection.Env = env
	return link
}

func redactDatabaseURL(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return value
	}
	if _, ok := parsed.User.Password(); !ok {
		return value
	}
	parsed.User = url.User(parsed.User.Username())
	return parsed.String()
}

func writeDatabaseLinkDetails(cmd *cobra.Command, link model.DatabaseLink) error {
	host := defaultString(link.Host, "-")
	if link.Connection.Host != "" {
		host = link.Connection.Host
		if link.Connection.Port > 0 {
			host += ":" + strconv.Itoa(link.Connection.Port)
		}
	}
	return writeTable(cmd, []string{"FIELD", "VALUE"}, [][]string{
		{"Link", link.ID},
		{"Site", defaultString(link.SiteName, link.SiteID)},
		{"Service", link.ServiceID},
		{"Engine", defaultString(string(link.Engine), "-")},
		{"Database", link.Database},
		{"Username", link.Username},
		{"Host", host},
		{"Primary", strconv.FormatBool(link.Primary)},
		{"Password", databasePasswordState(link.PasswordManaged)},
		{"Available", strconv.FormatBool(link.Available)},
		{"Error", defaultString(link.Error, "-")},
	})
}

func writeSecretTable(cmd *cobra.Command, label, value string) error {
	return writeTable(cmd, []string{"FIELD", "VALUE"}, [][]string{{label, value}})
}
