package multiuser

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestWorkspaceMembershipIsolation(t *testing.T) {
	databaseURL := os.Getenv("MULTIUSER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set MULTIUSER_TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	store, err := NewStore(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ownerEmail := "owner-" + uuid.NewString() + "@example.test"
	memberEmail := "member-" + uuid.NewString() + "@example.test"
	owner, err := store.CreateUser(context.Background(), ownerEmail, "hash")
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.CreateUser(context.Background(), memberEmail, "hash")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM workspace_members WHERE user_id = ANY($1)`, []uuid.UUID{owner.ID, member.ID})
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM workspaces WHERE owner_id = $1`, owner.ID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{owner.ID, member.ID})
	}()

	workspace, err := store.CreateWorkspace(context.Background(), owner.ID, "workspace-"+uuid.NewString()[:8], "Owner workspace", "test-"+uuid.NewString(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.WorkspaceForUser(context.Background(), member.ID, workspace.ID); !errors.Is(err, errNotFound) {
		t.Fatalf("unauthorized member received workspace: %v", err)
	}
	if _, err = store.WorkspaceForUser(context.Background(), owner.ID, workspace.ID); err != nil {
		t.Fatalf("owner lost workspace access: %v", err)
	}
}
