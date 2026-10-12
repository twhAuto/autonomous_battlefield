// Checks the AI gateway role migration keeps login-role membership and grants the full
// privilege set in every schema, including when the cluster-wide rename already ran.

package repo

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3/lock"
	"github.com/twh09/autonomous_battlefield/backend/internal/postgres"
)

func TestAIGatewayRoleMigrationKeepsMembershipAndGrants(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	ctx := context.Background()
	suffix := strings.ReplaceAll(randomUUID(t), "-", "")
	schema, login := "gateway_role_"+suffix, "gateway_login_"+suffix
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	defer db.Close()
	provider, err := postgres.NewMigrationProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 16); err != nil {
		t.Fatal(err)
	}
	// Roles are cluster-wide, so another package may already have renamed the role.
	// Hold the migration lock so no rename runs between the check and the grant.
	renamedEarlier := grantLegacyGatewayRole(ctx, t, admin, login)
	defer admin.ExecContext(ctx, `DROP ROLE `+login)
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if !renamedEarlier {
		requireRoleMember(ctx, t, admin, login, "battlefield_ai_gateway")
	}
	requireGatewayRoleGrants(ctx, t, admin, schema)
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal("migration rerun", err)
	}
}

func grantLegacyGatewayRole(ctx context.Context, t *testing.T, admin *sql.DB, login string) bool {
	t.Helper()
	conn, err := admin.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lock.DefaultLockID); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, lock.DefaultLockID)
	var renamed bool
	if err = conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='battlefield_ai_gateway')`).Scan(&renamed); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, `CREATE ROLE `+login+` LOGIN; GRANT battlefield_mcp TO `+login); err != nil {
		t.Fatal(err)
	}
	return renamed
}

func requireRoleMember(ctx context.Context, t *testing.T, admin *sql.DB, login, role string) {
	t.Helper()
	var member bool
	if err := admin.QueryRowContext(ctx, `SELECT pg_has_role($1, $2, 'MEMBER')`, login, role).Scan(&member); err != nil || !member {
		t.Fatalf("%s is not a member of %s: err=%v", login, role, err)
	}
}

func requireGatewayRoleGrants(ctx context.Context, t *testing.T, admin *sql.DB, schema string) {
	t.Helper()
	var canLogin bool
	if err := admin.QueryRowContext(ctx, `SELECT rolcanlogin FROM pg_roles WHERE rolname='battlefield_ai_gateway'`).Scan(&canLogin); err != nil || canLogin {
		t.Fatalf("gateway role must exist as NOLOGIN: canLogin=%v err=%v", canLogin, err)
	}
	for _, grant := range []struct{ table, privilege string }{
		{"battle_decisions", "SELECT"}, {"battle_decisions", "INSERT"}, {"battle_decisions", "UPDATE"},
		{"battle_controller_evaluation", "SELECT"},
		{"faction_background_generations", "SELECT"}, {"faction_background_generations", "UPDATE"},
		{"agent_execution_leases", "DELETE"},
	} {
		var granted bool
		if err := admin.QueryRowContext(ctx, `SELECT has_table_privilege('battlefield_ai_gateway', $1, $2)`, schema+"."+grant.table, grant.privilege).Scan(&granted); err != nil || !granted {
			t.Fatalf("gateway role lacks %s on %s: err=%v", grant.privilege, grant.table, err)
		}
	}
	var columnGrant, broadGrant bool
	if err := admin.QueryRowContext(ctx, `SELECT has_column_privilege('battlefield_ai_gateway', $1, 'ai_executions', 'UPDATE'), has_table_privilege('battlefield_ai_gateway', $1, 'UPDATE')`, schema+".battle_runs").Scan(&columnGrant, &broadGrant); err != nil || !columnGrant || broadGrant {
		t.Fatalf("battle_runs grants must stay column-scoped: column=%v table=%v err=%v", columnGrant, broadGrant, err)
	}
}
