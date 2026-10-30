package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func (a *App) newSiteDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Manage a site's database links",
	}
	cmd.AddCommand(
		a.newSiteDBListCmd(),
		a.newSiteDBLinkCmd(),
		a.newSiteDBUnlinkCmd(),
	)
	return cmd
}

func (a *App) newSiteDBListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <site>",
		Short: "List a site's database links",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "a site name or id is required", Remediation: "For example `devstack site db list blog`."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			links, err := c.ListSiteDatabases(ctx, ref)
			if err != nil {
				return err
			}
			if links == nil {
				links = []model.DatabaseLink{}
			}
			if a.jsonOutput {
				clean := make([]model.DatabaseLink, 0, len(links))
				for _, link := range links {
					clean = append(clean, sanitizeDatabaseLink(link))
				}
				return a.writeJSON(cmd, struct {
					Links []model.DatabaseLink `json:"links"`
				}{Links: clean})
			}
			return writeSiteDatabaseLinkTable(cmd, links)
		},
	}
}

func (a *App) newSiteDBLinkCmd() *cobra.Command {
	var service, database, userID string
	var primary, passwordStdin bool
	cmd := &cobra.Command{
		Use:   "link <site>",
		Short: "Link a database and user to a site",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			service = strings.TrimSpace(service)
			database = strings.TrimSpace(database)
			userID = strings.TrimSpace(userID)
			if ref == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "a site name or id is required", Remediation: "For example `devstack site db link blog --service mysql --database blog --user-id usr_blog`."}
			}
			if service == "" || database == "" || userID == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--service, --database, and --user-id are required", Remediation: "For example `devstack site db link blog --service mysql --database blog --user-id usr_blog`."}
			}
			if passwordStdin {
				return externalPasswordUnsupportedError()
			}
			if strings.EqualFold(service, "redis") {
				return &Error{Code: "DATABASE_UNSUPPORTED", Message: "Redis users are not site-linkable in v1", Remediation: "Use `devstack db user` to manage Redis ACL users."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			link, err := c.LinkSiteDatabase(ctx, ref, databases.LinkDatabaseRequest{
				ServiceID: service, Database: database, UserID: userID, Primary: primary,
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
	cmd.Flags().StringVar(&service, "service", "", "service id")
	cmd.Flags().StringVar(&database, "database", "", "database name")
	cmd.Flags().StringVar(&userID, "user-id", "", "opaque database user id")
	cmd.Flags().BoolVar(&primary, "primary", false, "make this the site's primary database link")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "unsupported in v1: external user passwords are deferred; fails without reading stdin")
	return cmd
}

func (a *App) newSiteDBUnlinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unlink <site> <link-id>",
		Short: "Remove a site database link",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			linkID := strings.TrimSpace(args[1])
			if ref == "" || linkID == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "a site and link id are required", Remediation: "For example `devstack site db unlink blog lnk_1 --yes`."}
			}
			if err := a.requireDestructiveConfirmation(cmd,
				fmt.Sprintf("Unlink database link %q from site %q? The database, user, and data are not dropped.", linkID, ref),
				"Re-run with --yes to confirm the unlink."); err != nil {
				return err
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			if err := c.UnlinkSiteDatabase(ctx, ref, linkID); err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					Site     string `json:"site"`
					LinkID   string `json:"link_id"`
					Unlinked bool   `json:"unlinked"`
				}{Site: ref, LinkID: linkID, Unlinked: true})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Unlinked database link %s from %s.\n", linkID, ref)
			return err
		},
	}
}

func writeSiteDatabaseLinkTable(cmd *cobra.Command, links []model.DatabaseLink) error {
	rows := make([][]string, 0, len(links))
	for _, raw := range links {
		link := sanitizeDatabaseLink(raw)
		host := defaultString(link.Host, "-")
		if link.Connection.Host != "" {
			host = link.Connection.Host
			if link.Connection.Port > 0 {
				host += ":" + strconv.Itoa(link.Connection.Port)
			}
		}
		status := "available"
		if !link.Available {
			status = "unavailable"
		}
		if link.Error != "" {
			status = link.Error
		}
		rows = append(rows, []string{
			link.ID,
			defaultString(string(link.Engine), "-"),
			host,
			link.Database,
			link.Username,
			strconv.FormatBool(link.Primary),
			databasePasswordState(link.PasswordManaged),
			status,
		})
	}
	return writeTable(cmd, []string{"LINK", "ENGINE", "HOST", "DATABASE", "USERNAME", "PRIMARY", "PASSWORD", "STATUS"}, rows)
}
