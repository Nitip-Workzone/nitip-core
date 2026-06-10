package notification

import (
	"context"
)

// Notifier defines the interface for sending push notifications.
type Notifier interface {
	SendToDevice(ctx context.Context, token, title, body string, data map[string]string) error
	SendToTopic(ctx context.Context, topic, title, body string, data map[string]string) error
	SendMulticast(ctx context.Context, tokens []string, title, body string, data map[string]string) error
}

// MockNotifier is a dummy implementation of Notifier for testing/development
// when FCM credentials are not available.
type MockNotifier struct {
}

func NewMockNotifier() *MockNotifier {
	return &MockNotifier{}
}

func (m *MockNotifier) SendToDevice(ctx context.Context, token, title, body string, data map[string]string) error {
	// Log only for debugging purposes
	return nil
}

func (m *MockNotifier) SendToTopic(ctx context.Context, topic, title, body string, data map[string]string) error {
	return nil
}

func (m *MockNotifier) SendMulticast(ctx context.Context, tokens []string, title, body string, data map[string]string) error {
	return nil
}
