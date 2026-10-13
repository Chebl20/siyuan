package multiuser

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNotFound = errors.New("not found")

type Store struct {
	pool *pgxpool.Pool
}

type User struct {
	ID                uuid.UUID
	Email             string
	PasswordHash      string
	ActiveWorkspaceID *uuid.UUID
}

type Workspace struct {
	ID            uuid.UUID
	OwnerID       uuid.UUID
	Slug          string
	Name          string
	Personal      bool
	ContainerName string
	Role          string
	Active        bool
}

func NewStore(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if _, err = pool.Exec(ctx, initialSchema); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (User, error) {
	var user User
	err := s.pool.QueryRow(ctx, `INSERT INTO users (email, email_normalized, password_hash) VALUES ($1, $2, $3) RETURNING id, email, password_hash, active_workspace_id`, email, normalizeEmail(email), passwordHash).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.ActiveWorkspaceID)
	return user, err
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	var user User
	err := s.pool.QueryRow(ctx, `SELECT id, email, password_hash, active_workspace_id FROM users WHERE email_normalized = $1`, normalizeEmail(email)).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.ActiveWorkspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, errNotFound
	}
	return user, err
}

func (s *Store) UserBySession(ctx context.Context, token string) (User, error) {
	var user User
	hash := tokenHash(token)
	err := s.pool.QueryRow(ctx, `SELECT u.id, u.email, u.password_hash, u.active_workspace_id FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = $1 AND s.expires_at > now()`, hash[:]).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.ActiveWorkspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, errNotFound
	}
	if err == nil {
		_, _ = s.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE token_hash = $1`, hash[:])
	}
	return user, err
}

func (s *Store) CreateSession(ctx context.Context, userID uuid.UUID, token string) error {
	hash := tokenHash(token)
	_, err := s.pool.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`, hash[:], userID, time.Now().Add(7*24*time.Hour))
	return err
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	hash := tokenHash(token)
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hash[:])
	return err
}

func (s *Store) CreateWorkspace(ctx context.Context, ownerID uuid.UUID, slug, name, containerName string, personal bool) (Workspace, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Workspace{}, err
	}
	defer tx.Rollback(ctx)
	var workspace Workspace
	err = tx.QueryRow(ctx, `INSERT INTO workspaces (owner_id, slug, name, container_name, personal) VALUES ($1, $2, $3, $4, $5) RETURNING id, owner_id, slug, name, personal, container_name`, ownerID, slug, name, containerName, personal).Scan(&workspace.ID, &workspace.OwnerID, &workspace.Slug, &workspace.Name, &workspace.Personal, &workspace.ContainerName)
	if err != nil {
		return Workspace{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspace.ID, ownerID); err != nil {
		return Workspace{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET active_workspace_id = COALESCE(active_workspace_id, $1), updated_at = now() WHERE id = $2`, workspace.ID, ownerID); err != nil {
		return Workspace{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Workspace{}, err
	}
	workspace.Role = "owner"
	return workspace, nil
}

func (s *Store) WorkspacesForUser(ctx context.Context, userID uuid.UUID) ([]Workspace, error) {
	rows, err := s.pool.Query(ctx, `SELECT w.id, w.owner_id, w.slug, w.name, w.personal, w.container_name, m.role FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id WHERE m.user_id = $1 AND w.deleted_at IS NULL ORDER BY w.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var workspaces []Workspace
	for rows.Next() {
		var workspace Workspace
		if err = rows.Scan(&workspace.ID, &workspace.OwnerID, &workspace.Slug, &workspace.Name, &workspace.Personal, &workspace.ContainerName, &workspace.Role); err != nil {
			return nil, err
		}
		workspaces = append(workspaces, workspace)
	}
	return workspaces, rows.Err()
}

func (s *Store) WorkspaceForUser(ctx context.Context, userID uuid.UUID, workspaceID uuid.UUID) (Workspace, error) {
	return s.workspaceQuery(ctx, `w.id = $2`, userID, workspaceID)
}

func (s *Store) WorkspaceForUserBySlug(ctx context.Context, userID uuid.UUID, slug string) (Workspace, error) {
	return s.workspaceQuery(ctx, `w.slug = $2`, userID, strings.ToLower(slug))
}

func (s *Store) workspaceQuery(ctx context.Context, condition string, userID uuid.UUID, value any) (Workspace, error) {
	var workspace Workspace
	query := `SELECT w.id, w.owner_id, w.slug, w.name, w.personal, w.container_name, m.role FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id WHERE m.user_id = $1 AND w.deleted_at IS NULL AND ` + condition
	err := s.pool.QueryRow(ctx, query, userID, value).Scan(&workspace.ID, &workspace.OwnerID, &workspace.Slug, &workspace.Name, &workspace.Personal, &workspace.ContainerName, &workspace.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, errNotFound
	}
	return workspace, err
}

func (s *Store) RenameWorkspace(ctx context.Context, workspaceID, userID uuid.UUID, name string) error {
	command, err := s.pool.Exec(ctx, `UPDATE workspaces SET name = $1, updated_at = now() WHERE id = $2 AND owner_id = $3 AND deleted_at IS NULL`, name, workspaceID, userID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errNotFound
	}
	return nil
}

func (s *Store) SoftDeleteWorkspace(ctx context.Context, workspaceID, userID uuid.UUID) (Workspace, error) {
	workspace, err := s.WorkspaceForUser(ctx, userID, workspaceID)
	if err != nil || workspace.OwnerID != userID {
		return Workspace{}, errNotFound
	}
	command, err := s.pool.Exec(ctx, `UPDATE workspaces SET deleted_at = now(), updated_at = now() WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL`, workspaceID, userID)
	if err != nil || command.RowsAffected() != 1 {
		return Workspace{}, errNotFound
	}
	_, _ = s.pool.Exec(ctx, `UPDATE users SET active_workspace_id = NULL WHERE id = $1 AND active_workspace_id = $2`, userID, workspaceID)
	return workspace, nil
}

func (s *Store) SetActiveWorkspace(ctx context.Context, userID, workspaceID uuid.UUID) error {
	command, err := s.pool.Exec(ctx, `UPDATE users SET active_workspace_id = $2, updated_at = now() WHERE id = $1 AND EXISTS (SELECT 1 FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id WHERE m.user_id = $1 AND m.workspace_id = $2 AND w.deleted_at IS NULL)`, userID, workspaceID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errNotFound
	}
	return nil
}

func (s *Store) CreateResetToken(ctx context.Context, userID uuid.UUID, token string) error {
	hash := tokenHash(token)
	_, err := s.pool.Exec(ctx, `INSERT INTO password_reset_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`, hash[:], userID, time.Now().Add(30*time.Minute))
	return err
}

func (s *Store) ResetPassword(ctx context.Context, token, passwordHash string) (uuid.UUID, error) {
	hash := tokenHash(token)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	var userID uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE password_reset_tokens SET used_at = now() WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now() RETURNING user_id`, hash[:]).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2`, passwordHash, userID); err != nil {
		return uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return uuid.Nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return userID, nil
}

func (s *Store) Audit(ctx context.Context, userID *uuid.UUID, event string, ip string) {
	_, _ = s.pool.Exec(ctx, `INSERT INTO audit_events (user_id, event_type, remote_ip) VALUES ($1, $2, NULLIF($3, '')::inet)`, userID, event, ip)
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func tokenHash(token string) [32]byte {
	return sha256.Sum256([]byte(token))
}
