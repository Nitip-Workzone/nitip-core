package chat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	notifDomain "github.com/codecoffy/nitip-core/internal/domain/notification"
	"github.com/codecoffy/nitip-core/internal/domain/order"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/storage"
	"github.com/codecoffy/nitip-core/internal/notification"
	"github.com/google/uuid"
)

var (
	ErrUnauthorized  = errors.New("anda bukan peserta dalam pesanan ini")
	ErrOrderNotFound = errors.New("pesanan tidak ditemukan")
)

type Service interface {
	SendMessage(ctx context.Context, orderID, senderID uuid.UUID, content, msgType string) (*ChatMessage, error)
	GetHistory(ctx context.Context, orderID, userID uuid.UUID, limit int) ([]ChatMessage, error)
	UploadImage(ctx context.Context, orderID, userID uuid.UUID, filename string, content io.Reader) (string, error)
	RegisterClient(orderID string, client *Client)
	UnregisterClient(orderID string, userID uuid.UUID)
}

type service struct {
	repo      Repository
	orderRepo order.Repository
	userRepo  user.Repository
	hub       *Hub
	fcm       notification.Notifier
	notifSvc  notifDomain.Service
	storage   storage.Storage
}

func NewService(repo Repository, orderRepo order.Repository, userRepo user.Repository, hub *Hub, fcm notification.Notifier, notifSvc notifDomain.Service, storage storage.Storage) Service {
	return &service{
		repo:      repo,
		orderRepo: orderRepo,
		userRepo:  userRepo,
		hub:       hub,
		fcm:       fcm,
		notifSvc:  notifSvc,
		storage:   storage,
	}
}

func (s *service) SendMessage(ctx context.Context, orderID, senderID uuid.UUID, content, msgType string) (*ChatMessage, error) {
	// 1. Verify order and participants
	ord, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		return nil, ErrOrderNotFound
	}

	isRunner := ord.RunnerID != nil && *ord.RunnerID == senderID
	isRequester := ord.RequesterID == senderID

	if !isRunner && !isRequester {
		return nil, ErrUnauthorized
	}

	// Determine recipient
	var recipientID uuid.UUID
	if isRunner {
		recipientID = ord.RequesterID
	} else {
		if ord.RunnerID != nil {
			recipientID = *ord.RunnerID
		}
	}

	// 2. Create message object
	msg := &ChatMessage{
		OrderID:   orderID,
		SenderID:  senderID,
		Content:   content,
		Type:      msgType,
		IsRead:    false,
		CreatedAt: time.Now(),
	}

	// Determine role
	if isRequester {
		msg.SenderRole = user.RoleRequester
	} else if isRunner {
		msg.SenderRole = user.RoleRunner
	}

	// 3. Save to Firestore
	if err := s.repo.Save(ctx, msg); err != nil {
		return nil, err
	}

	// Sign URL if it's an image before broadcasting/returning
	s.signURLs(ctx, msg)

	// 4. Real-time Broadcast via Hub
	if s.hub != nil {
		s.hub.Broadcast(orderID.String(), msg)
	}

	// 5. Push Notification if recipient is offline
	if s.hub != nil && s.fcm != nil && recipientID != uuid.Nil {
		if !s.hub.IsUserOnline(orderID.String(), recipientID) {
			recipient, err := s.userRepo.FindByID(ctx, recipientID)
			if err == nil && recipient.FcmToken != nil && *recipient.FcmToken != "" {
				sender, _ := s.userRepo.FindByID(ctx, senderID)
				title := "Pesan Baru"
				if sender != nil {
					title = "Pesan dari " + sender.Name
				}
				body := content
				if msgType == "image" {
					body = "[Gambar]"
				}
				_ = s.fcm.SendToDevice(ctx, *recipient.FcmToken, title, body, map[string]string{
					"type":     "chat",
					"order_id": orderID.String(),
				})
			}
		}

		// Create In-App Notification (Always, for history)
		sender, _ := s.userRepo.FindByID(ctx, senderID)
		title := "Pesan Baru"
		if sender != nil {
			title = "Pesan dari " + sender.Name
		}
		body := content
		if msgType == "image" {
			body = "[Gambar]"
		}
		_ = s.notifSvc.CreateNotification(ctx, notifDomain.CreateNotificationRequest{
			UserID:  recipientID,
			Title:   title,
			Message: body,
			Type:    "chat",
			Metadata: map[string]interface{}{
				"order_id": orderID,
			},
		})
	}

	return msg, nil
}

func (s *service) UploadImage(ctx context.Context, orderID, userID uuid.UUID, filename string, content io.Reader) (string, error) {
	// 1. Verify membership
	ord, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		return "", ErrOrderNotFound
	}

	isRunner := ord.RunnerID != nil && *ord.RunnerID == userID
	isRequester := ord.RequesterID == userID

	if !isRunner && !isRequester {
		return "", ErrUnauthorized
	}

	// 2. Upload to Storage (returns relative path/key)
	var buf bytes.Buffer
	size, err := io.Copy(&buf, content)
	if err != nil {
		return "", fmt.Errorf("failed to read chat image content: %w", err)
	}

	contentType := "image/jpeg"
	limit := 512
	if buf.Len() < limit {
		limit = buf.Len()
	}
	if limit > 0 {
		contentType = http.DetectContentType(buf.Bytes()[:limit])
		if contentType == "application/octet-stream" {
			contentType = "image/jpeg"
		}
	}

	objectKey := fmt.Sprintf("chat/%s/%s", orderID.String(), filename)
	path, err := s.storage.Upload(ctx, objectKey, &buf, size, contentType)
	if err != nil {
		return "", err
	}

	return path, nil
}

func (s *service) GetHistory(ctx context.Context, orderID, userID uuid.UUID, limit int) ([]ChatMessage, error) {
	// 1. Verify membership
	ord, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		return nil, ErrOrderNotFound
	}

	isRunner := ord.RunnerID != nil && *ord.RunnerID == userID
	isRequester := ord.RequesterID == userID

	if !isRunner && !isRequester {
		return nil, ErrUnauthorized
	}

	if limit <= 0 {
		limit = 50
	}

	// 2. Fetch history
	messages, err := s.repo.GetByOrderID(ctx, orderID, limit)
	if err != nil {
		return nil, err
	}

	// 3. Mark others' messages as read and enrich with roles
	_ = s.repo.MarkAsRead(ctx, orderID, userID)

	for i := range messages {
		s.signURLs(ctx, &messages[i])
		if ord.RequesterID == messages[i].SenderID {
			messages[i].SenderRole = user.RoleRequester
		} else if ord.RunnerID != nil && *ord.RunnerID == messages[i].SenderID {
			messages[i].SenderRole = user.RoleRunner
		}
	}

	return messages, nil
}

func (s *service) RegisterClient(orderID string, client *Client) {
	if s.hub != nil {
		s.hub.Register(orderID, client)
	}
}

func (s *service) UnregisterClient(orderID string, userID uuid.UUID) {
	if s.hub != nil {
		s.hub.Unregister(orderID, userID)
	}
}
func (s *service) signURLs(ctx context.Context, msg *ChatMessage) {
	if msg == nil || msg.Type != "image" || msg.Content == "" {
		return
	}
	// Sign content if it's an image path
	signed, err := s.storage.SignedURL(ctx, msg.Content, 1*time.Hour)
	if err == nil {
		msg.Content = signed
	}
}
