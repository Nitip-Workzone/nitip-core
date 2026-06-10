package systemconfig

import (
	"context"
	"sync"
)

type Service interface {
	GetValue(ctx context.Context, key string, defaultValue string) string
	GetAll(ctx context.Context) ([]Config, error)
	SetValue(ctx context.Context, key, value, description string) error
}

type service struct {
	repo  Repository
	cache sync.Map
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func (s *service) GetValue(ctx context.Context, key string, defaultValue string) string {
	if val, ok := s.cache.Load(key); ok {
		return val.(string)
	}

	cfg, err := s.repo.Get(ctx, key)
	if err != nil {
		return defaultValue
	}

	s.cache.Store(key, cfg.Value)
	return cfg.Value
}

func (s *service) GetAll(ctx context.Context) ([]Config, error) {
	return s.repo.GetAll(ctx)
}

func (s *service) SetValue(ctx context.Context, key, value, description string) error {
	cfg := &Config{
		Key:         key,
		Value:       value,
		Description: description,
	}
	err := s.repo.Set(ctx, cfg)
	if err == nil {
		s.cache.Store(key, value)
	}
	return err
}
