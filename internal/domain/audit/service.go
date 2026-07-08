package audit

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Service interface {
	Log(ctx context.Context, userID *uuid.UUID, action, resource, resourceID string, oldValues, newValues interface{}, ip, ua string)
	LogWithDB(ctx context.Context, db bun.IDB, userID *uuid.UUID, action, resource, resourceID string, oldValues, newValues interface{}, ip, ua string)
	List(ctx context.Context, offset, limit int, action string) ([]AuditLog, int, error)
}

type service struct {
	repo Repository
	db   *bun.DB
}

func NewService(repo Repository, db *bun.DB) Service {
	return &service{repo: repo, db: db}
}

func (s *service) List(ctx context.Context, offset, limit int, action string) ([]AuditLog, int, error) {
	return s.repo.List(ctx, s.db, offset, limit, action)
}

func (s *service) Log(ctx context.Context, userID *uuid.UUID, action, resource, resourceID string, oldValues, newValues interface{}, ip, ua string) {
	s.LogWithDB(ctx, s.db, userID, action, resource, resourceID, oldValues, newValues, ip, ua)
}

func (s *service) LogWithDB(ctx context.Context, db bun.IDB, userID *uuid.UUID, action, resource, resourceID string, oldValues, newValues interface{}, ip, ua string) {
	auditLog := &AuditLog{
		UserID:     userID,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		OldValues:  oldValues,
		NewValues:  newValues,
		IPAddress:  ip,
		UserAgent:  ua,
		CreatedAt:  time.Now(),
	}

	// We run this in a background-like manner or just ignore error for audit logging
	// to avoid blocking the main business logic
	if err := s.repo.Create(ctx, db, auditLog); err != nil {
		log.Printf("[AUDIT] Error creating log: %v", err)
	}
}
