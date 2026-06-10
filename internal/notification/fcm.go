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

// SendMulticast sends to multiple device tokens at once.
func (f *FCM) SendMulticast(ctx context.Context, tokens []string, title, body string, data map[string]string) error {
	msg := &messaging.MulticastMessage{
		Tokens: tokens,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Data: data,
	}

	resp, err := f.client.SendEachForMulticast(ctx, msg)
	if err != nil {
		return fmt.Errorf("FCM multicast failed: %w", err)
	}

	f.logger.Info("FCM multicast sent",
		zap.Int("success_count", resp.SuccessCount),
		zap.Int("failure_count", resp.FailureCount),
	)
	return nil
}
