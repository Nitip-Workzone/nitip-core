package firebase

import (
	"context"
	"fmt"

	"os"

	firebase "firebase.google.com/go/v4"
	"github.com/codecoffy/nitip-core/config"
	"google.golang.org/api/option"
)

func NewApp(cfg *config.Config) (*firebase.App, error) {
	ctx := context.Background()

	// If no credentials file provided or file doesn't exist, return nil without error
	// to allow dummy handling in the domain layers.
	if cfg.FirebaseCredentialsFile == "" {
		return nil, nil
	}

	if _, err := os.Stat(cfg.FirebaseCredentialsFile); os.IsNotExist(err) {
		return nil, nil
	}

	app, err := firebase.NewApp(ctx, nil, option.WithCredentialsFile(cfg.FirebaseCredentialsFile))
	if err != nil {
		return nil, fmt.Errorf("failed to init firebase app: %w", err)
	}
	return app, nil
}
