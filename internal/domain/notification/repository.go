package notification

import (
	"context"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	FindAllByUserID(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]Notification, error)
	GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error)
	Create(ctx context.Context, notification *Notification) error
	MarkAsRead(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
	MarkAllAsRead(ctx context.Context, userID uuid.UUID) error
	DeleteOld(ctx context.Context, cutoffDays int) (int64, error)
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) FindAllByUserID(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]Notification, error) {
	var notifications []Notification
	err := r.db.NewSelect().
		Model(&notifications).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Scan(ctx)
	return notifications, err
}

func (r *repository) GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	count, err := r.db.NewSelect().
		Model((*Notification)(nil)).
		Where("user_id = ? AND is_read = false", userID).
		Count(ctx)
	return count, err
}

func (r *repository) Create(ctx context.Context, notification *Notification) error {
	_, err := r.db.NewInsert().Model(notification).Exec(ctx)
	return err
}

func (r *repository) MarkAsRead(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	_, err := r.db.NewUpdate().
		Model((*Notification)(nil)).
		Set("is_read = ?", true).
		Where("id = ? AND user_id = ?", id, userID).
		Exec(ctx)
	return err
}

func (r *repository) MarkAllAsRead(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.NewUpdate().
		Model((*Notification)(nil)).
		Set("is_read = ?", true).
		Where("user_id = ?", userID).
		Exec(ctx)
	return err
}

func (r *repository) DeleteOld(ctx context.Context, cutoffDays int) (int64, error) {
	res, err := r.db.NewDelete().
		Model((*Notification)(nil)).
		Where("created_at < NOW() - INTERVAL '? days'", cutoffDays).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	count, _ := res.RowsAffected()
	return count, nil
}
