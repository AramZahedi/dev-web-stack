package dbengine

import "github.com/AramZahedi/dev-web-stack/internal/model"

// NewMariaDB returns the MariaDB engine. It shares the MySQL SQL dialect but
// uses the mariadb client and mariadb-dump tools shipped in the image.
func NewMariaDB() Engine {
	return &mysqlEngine{id: model.DatabaseMariaDB, client: "mariadb", dump: "mariadb-dump"}
}
