package chat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	notifDomain "github.com/codecoffy/nitip-core/internal/domain/notification"
	"github.com/codecoffy/nitip-core/internal/domain/order"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/notification"
	"github.com/codecoffy/nitip-core/internal/storage"
	"github.com/codecoffy/nitip-core/pkg/fileutil"
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

	// 5. Push Notification via dispatcher — per-device bucket 20/10m, collapse_id chat_{orderID} prevents burst limit hit
	// Previously bypassed dispatcher via direct SendToDevice — risk hit 20 burst if chat spam
	// Now uses in-app notification + FCM via dispatcher if exists, else fallback direct with collapse
	if recipientID != uuid.Nil {
		sender, _ := s.userRepo.FindByID(ctx, senderID)
		title := "Pesan Baru"
		if sender != nil {
			title = "Pesan dari " + sender.Name
		}
		body := content
		if msgType == "image" {
			body = "[Gambar]"
		}
		// Always create in-app notification history
		_ = s.notifSvc.CreateNotification(ctx, notifDomain.CreateNotificationRequest{
			UserID:  recipientID,
			Title:   title,
			Message: body,
			Type:    "chat",
			Metadata: map[string]interface{}{
				"order_id": orderID,
			},
		})

		// FCM only if recipient offline — per-device bucket 20/10m + collapse_id chat_{orderID} prevents burst limit hit
		shouldNotify := true
		if s.hub != nil {
			if s.hub.IsUserOnline(orderID.String(), recipientID) {
				// User online via WebSocket/SSE — skip FCM to save quota, chat already broadcast via Hub
				shouldNotify = false
			}
		}
		if shouldNotify && s.fcm != nil {
			recipient, err := s.userRepo.FindByID(ctx, recipientID)
			if err == nil && recipient.FcmToken != nil && *recipient.FcmToken != "" {
				_ = s.fcm.SendToDeviceWithCollapse(ctx, *recipient.FcmToken, title, body,
					map[string]string{"type": "chat", "order_id": orderID.String()},
					fmt.Sprintf("chat_%s", orderID.String()))
			}
		}
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

	// 2. Compress <1MB with bounded concurrency (Lighthouse 2C4G) before upload — prevent VPS OOM + save COS egress
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, content); err != nil {
		return "", fmt.Errorf("failed to read chat image content: %w", err)
	}

	// Compress <1MB
	compressed, compSize, compErr := fileutil.CompressToLimit(bytes.NewReader(buf.Bytes()), 1280, fileutil.DefaultMaxUpload)
	if compErr != nil {
		return "", fmt.Errorf("gagal mengompresi gambar chat: %w", compErr)
	}
	if compSize > fileutil.DefaultMaxUpload {
		return "", fmt.Errorf("gambar chat masih >1MB (%dKB), coba foto lebih kecil", compSize/1024)
	}

	// Penamaan clean tanpa nama aneh — chat/<orderID>/<uuid8>_<nano>.jpg (tanpa filename asli)
	objectKey := fmt.Sprintf("chat/%s/%s_%d.jpg", orderID.String(), uuid.New().String()[:8], time.Now().UnixNano())
	path, err := s.storage.Upload(ctx, objectKey, compressed, compSize, "image/jpeg")
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
