// Checks connection identities are removed from error text while application text and
// SQLSTATE codes remain.

package redact

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestErrorRemovesPgxConnectionIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Port 1 refuses immediately, producing a real *pgconn.ConnectError.
	_, err := pgconn.Connect(ctx, "postgres://secret_user:secret_password@127.0.0.1:1/secret_database?connect_timeout=2")
	var connect *pgconn.ConnectError
	if !errors.As(err, &connect) {
		t.Fatalf("expected a ConnectError, got %T: %v", err, err)
	}
	wrapped := fmt.Errorf("open database: %w", err)
	got := Error(wrapped)
	for _, leaked := range []string{"secret_user", "secret_password", "secret_database", "127.0.0.1"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted text %q still contains %q", got, leaked)
		}
	}
	if got != "open database: failed to connect to the database" {
		t.Fatalf("redacted text = %q", got)
	}
}

func TestErrorRemovesNetworkAddressesAndHostnames(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 5432}, Err: errors.New("connect: connection refused")}
	lookup := &net.DNSError{Err: "no such host", Name: "battlefield-postgres.internal"}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("ping: %w", refused), "ping: dial tcp: connect: connection refused"},
		{&net.OpError{Op: "dial", Net: "tcp", Err: lookup}, "dial tcp: lookup failed: no such host"},
		{fmt.Errorf("resolve: %w", lookup), "resolve: lookup failed: no such host"},
	} {
		if got := Error(tc.err); got != tc.want {
			t.Fatalf("Error(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestErrorKeepsApplicationTextAndSQLState(t *testing.T) {
	database := &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint \"factions_pkey\""}
	for _, err := range []error{
		errors.New("invalid strategy plan: name is required"),
		fmt.Errorf("create faction: %w", database),
	} {
		if got := Error(err); got != err.Error() {
			t.Fatalf("Error(%v) = %q, want it unchanged", err, got)
		}
	}
	if Error(nil) != "" {
		t.Fatal("Error(nil) is not empty")
	}
}
