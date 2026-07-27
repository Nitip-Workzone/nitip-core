package notification

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
)

type Service interface {
	GetUserNotifications(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]Notification, error)
	GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error)
	CreateNotification(ctx context.Context, req CreateNotificationRequest) error
	MarkAsRead(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
	MarkAllAsRead(ctx context.Context, userID uuid.UUID) error
	StartCleanupWorker(ctx context.Context)
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

func (s *service) StartCleanupWorker(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				s.cleanupOldNotifications(context.Background())
			}
		}
	}()
	// Initial cleanup after 10 minutes
	go func() {
		time.Sleep(10 * time.Minute)
		s.cleanupOldNotifications(context.Background())
	}()
}

func (s *service) cleanupOldNotifications(ctx context.Context) {
	// Delete notifications older than 90 days
	count, err := s.repo.DeleteOld(ctx, 90)
	if err != nil {
		log.Printf("[NOTIFICATION-CLEANUP-ERROR] Failed to cleanup old notifications: %v", err)
		return
	}
	if count > 0 {
		log.Printf("[NOTIFICATION-CLEANUP] Deleted %d old notifications (>90 days)", count)
	}
}
