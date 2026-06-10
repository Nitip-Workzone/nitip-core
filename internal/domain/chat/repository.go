package chat

import (
	"context"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	Save(ctx context.Context, msg *ChatMessage) error
	GetByOrderID(ctx context.Context, orderID uuid.UUID, limit int) ([]ChatMessage, error)
	MarkAsRead(ctx context.Context, orderID uuid.UUID, userID uuid.UUID) error
}

type postgresRepository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &postgresRepository{db: db}
}

func (r *postgresRepository) Save(ctx context.Context, msg *ChatMessage) error {
	_, err := r.db.NewInsert().Model(msg).Exec(ctx)
	return err
}

func (r *postgresRepository) GetByOrderID(ctx context.Context, orderID uuid.UUID, limit int) ([]ChatMessage, error) {
	var messages []ChatMessage
	err := r.db.NewSelect().
		Model(&messages).
		Where("order_id = ?", orderID).
		Order("created_at DESC").
		Limit(limit).
		Scan(ctx)

	if err != nil {
		return nil, err
	}

	// Reverse to show in chronological order for UI
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}

func (r *postgresRepository) MarkAsRead(ctx context.Context, orderID uuid.UUID, userID uuid.UUID) error {
	_, err := r.db.NewUpdate().
		Model((*ChatMessage)(nil)).
		Set("is_read = ?", true).
		Where("order_id = ?", orderID).
		Where("sender_id != ?", userID).
		Where("is_read = ?", false).
		Exec(ctx)
	return err
}
