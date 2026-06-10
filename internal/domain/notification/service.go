package notification

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Service interface {
	GetUserNotifications(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]Notification, error)
	GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error)
	CreateNotification(ctx context.Context, req CreateNotificationRequest) error
	MarkAsRead(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
	MarkAllAsRead(ctx context.Context, userID uuid.UUID) error
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func (s *service) GetUserNotifications(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]Notification, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.repo.FindAllByUserID(ctx, userID, limit, offset)
}

func (s *service) GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	return s.repo.GetUnreadCount(ctx, userID)
}

func (s *service) CreateNotification(ctx context.Context, req CreateNotificationRequest) error {
	notif := &Notification{
		ID:        uuid.New(),
		UserID:    req.UserID,
		Title:     req.Title,
		Message:   req.Message,
		Type:      req.Type,
		IsRead:    false,
		Metadata:  req.Metadata,
		CreatedAt: time.Now(),
	}
	return s.repo.Create(ctx, notif)
}

func (s *service) MarkAsRead(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	return s.repo.MarkAsRead(ctx, id, userID)
}

func (s *service) MarkAllAsRead(ctx context.Context, userID uuid.UUID) error {
	return s.repo.MarkAllAsRead(ctx, userID)
}
