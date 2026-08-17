package notification

import (
	"context"
	"fmt"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"go.uber.org/zap"
)

type FCM struct {
	client *messaging.Client
	logger *zap.Logger
}

func NewFCM(app *firebase.App, logger *zap.Logger) (Notifier, error) {
	if app == nil {
		logger.Warn("firebase FCM config missing, using MockNotifier")
		return NewMockNotifier(), nil
	}

	ctx := context.Background()

	client, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get FCM client: %w", err)
	}

	logger.Info("firebase FCM initialized")
	return &FCM{client: client, logger: logger}, nil
}

// SendToDevice sends a push notification to a single device token.
func (f *FCM) SendToDevice(ctx context.Context, token, title, body string, data map[string]string) error {
	msg := &messaging.Message{
		Token: token,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Data: data,
	}

	resp, err := f.client.Send(ctx, msg)
	if err != nil {
		return fmt.Errorf("FCM send to device failed: %w", err)
	}

	f.logger.Info("FCM notification sent", zap.String("message_id", resp), zap.String("token", token))
	return nil
}

// SendToTopic sends a push notification to all subscribers of a topic.
func (f *FCM) SendToTopic(ctx context.Context, topic, title, body string, data map[string]string) error {
	msg := &messaging.Message{
		Topic: topic,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Data: data,
	}

	resp, err := f.client.Send(ctx, msg)
	if err != nil {
		return fmt.Errorf("FCM send to topic failed: %w", err)
	}

	f.logger.Info("FCM topic notification sent", zap.String("message_id", resp), zap.String("topic", topic))
	return nil
}

// SendToDeviceWithCollapse sends with collapse_id to avoid per-device burst 20 violation
func (f *FCM) SendToDeviceWithCollapse(ctx context.Context, token, title, body string, data map[string]string, collapseID string) error {
	msg := &messaging.Message{
		Token: token,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Data: data,
		Android: &messaging.AndroidConfig{
			CollapseKey: collapseID,
			Priority:    "high",
		},
		APNS: &messaging.APNSConfig{
			Headers: map[string]string{
				"apns-collapse-id": collapseID,
			},
		},
	}
	resp, err := f.client.Send(ctx, msg)
	if err != nil {
		return fmt.Errorf("FCM send with collapse failed: %w", err)
	}
	f.logger.Info("FCM notification sent with collapse", zap.String("message_id", resp), zap.String("collapse_id", collapseID))
	return nil
}

// SendMulticast sends to multiple device tokens at once.
func (f *FCM) SendMulticast(ctx context.Context, tokens []string, title, body string, data map[string]string) error {
	// Batch 500 per Firebase limit to stay under 600k/min
	batchSize := FCMMaxBatch
	if batchSize <= 0 {
		batchSize = 500
	}
	var lastErr error
	for i := 0; i < len(tokens); i += batchSize {
		end := i + batchSize
		if end > len(tokens) {
			end = len(tokens)
		}
		batch := tokens[i:end]
		msg := &messaging.MulticastMessage{
			Tokens: batch,
			Notification: &messaging.Notification{
				Title: title,
				Body:  body,
			},
			Data: data,
		}
		resp, err := f.client.SendEachForMulticast(ctx, msg)
		if err != nil {
			lastErr = fmt.Errorf("FCM multicast failed: %w", err)
			continue
		}
		f.logger.Info("FCM multicast sent",
			zap.Int("success_count", resp.SuccessCount),
			zap.Int("failure_count", resp.FailureCount),
			zap.Int("batch", i/batchSize),
		)
	}
	return lastErr
}
