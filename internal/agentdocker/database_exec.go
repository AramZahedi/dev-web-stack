package agentdocker

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// databaseSQLCommand is the fixed client used to execute whitelisted SQL on a
// service. SQL is always delivered on stdin; no caller SQL reaches argv.
func databaseSQLCommand(engine model.DatabaseEngine) ([]string, error) {
	switch engine {
	case model.DatabaseMySQL:
		return []string{"mysql", "--batch", "--skip-column-names"}, nil
	case model.DatabaseMariaDB:
		return []string{"mariadb", "--batch", "--skip-column-names"}, nil
	case model.DatabasePostgreSQL:
		return []string{
			"psql", "--no-psqlrc", "--set=ON_ERROR_STOP=1",
			"--username=postgres", "--dbname=postgres",
			"--tuples-only", "--no-align",
		}, nil
	default:
		return nil, fmt.Errorf("%w: no SQL client for %s", dbengine.ErrUnsupported, engine)
	}
}

func (d *Docker) runDatabaseSQL(ctx context.Context, serviceID string, command []string, input []byte) ([]byte, error) {
	return d.composeExecSQL(ctx, serviceID, input, command)
}

func (d *Docker) runDatabaseStream(ctx context.Context, serviceID string, command []string) (io.ReadCloser, error) {
	return d.composeExecStream(ctx, serviceID, command)
}

func (d *Docker) runDatabaseInput(ctx context.Context, serviceID string, command []string, input io.Reader) error {
	return d.composeExecInput(ctx, serviceID, input, command)
}

// terminateDatabaseSessions asks the engine for its fixed session-termination
// statement and executes it. MySQL/MariaDB cannot run KILL from a SELECT, so
// their statement returns one KILL per row and the agent executes each row.
func (d *Docker) terminateDatabaseSessions(ctx context.Context, serviceID string, engine dbengine.Engine, database string) error {
	statement, err := engine.TerminateSessionsSQL(database)
	if err != nil {
		return err
	}
	command, err := databaseSQLCommand(engine.ID())
	if err != nil {
		return err
	}
	output, err := d.runDatabaseSQL(ctx, serviceID, command, []byte(statement))
	if err != nil {
		return fmt.Errorf("agentdocker: terminate sessions on %s: %w", database, err)
	}
	switch engine.ID() {
	case model.DatabaseMySQL, model.DatabaseMariaDB:
		for _, line := range strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if !strings.HasPrefix(strings.ToUpper(line), "KILL ") {
				return fmt.Errorf("agentdocker: unexpected session statement %q", line)
			}
			if _, err := d.runDatabaseSQL(ctx, serviceID, command, []byte(line)); err != nil {
				return fmt.Errorf("agentdocker: terminate session: %w", err)
			}
		}
	}
	return nil
}

// requireActiveSQLService validates the service and rejects stopped containers
// before a mutation builds any engine command.
func (d *Docker) requireActiveSQLService(ctx context.Context, serviceID string) (dbengine.Engine, error) {
	engine, _, err := d.databaseEngine(serviceID)
	if err != nil {
		return nil, err
	}
	if engine.ID() == model.DatabaseRedis {
		return nil, fmt.Errorf("%w: redis has no SQL database operations", dbengine.ErrUnsupported)
	}
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return nil, err
	}
	return engine, nil
}
