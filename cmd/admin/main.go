package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/database"
	"github.com/codecoffy/nitip-core/internal/domain/audit"
	"github.com/codecoffy/nitip-core/internal/domain/auth"
	systemconfig "github.com/codecoffy/nitip-core/internal/domain/config"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	applogger "github.com/codecoffy/nitip-core/internal/logger"
	jwtLib "github.com/codecoffy/nitip-core/pkg/jwt"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	cfg := config.Load()
	logger, _ := applogger.New(cfg.IsDevelopment())
	defer logger.Sync() //nolint:errcheck

	db, err := database.New(cfg, logger)
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Init Repos & Services
	auditRepo := audit.NewRepository(db)
	auditSvc := audit.NewService(auditRepo, db)

	cfgRepo := systemconfig.NewRepository(db)
	cfgSvc := systemconfig.NewService(cfgRepo)

	// Init Cache
	rCache, err := cache.NewRedis(cfg, logger)
	if err != nil {
		log.Fatalf("failed to connect redis: %v", err)
	}

	userRepo := user.NewRepository(db)
	userSvc := user.NewService(userRepo, rCache, auditSvc, nil)

	switch command {
	case "get":
		if len(os.Args) < 3 {
			log.Fatal("usage: get <key>")
		}
		key := os.Args[2]
		val := cfgSvc.GetValue(ctx, key, "<NOT_FOUND>")
		fmt.Printf("%s = %s\n", key, val)

	case "set":
		if len(os.Args) < 4 {
			log.Fatal("usage: set <key> <value> [description]")
		}
		key := os.Args[2]
		val := os.Args[3]
		desc := ""
		if len(os.Args) >= 5 {
			desc = os.Args[4]
		}

		err := cfgSvc.SetValue(ctx, key, val, desc)
		if err != nil {
			log.Fatalf("failed to set %s: %v", key, err)
		}
		fmt.Printf("Updated %s = %s\n", key, val)

	case "list":
		cfgs, err := cfgSvc.GetAll(ctx)
		if err != nil {
			log.Fatalf("failed to list configs: %v", err)
		}
		fmt.Printf("\n--- System Configs ---\n")
		for _, c := range cfgs {
			fmt.Printf("%s = %s (%s)\n", c.Key, c.Value, c.Description)
		}
		fmt.Println("----------------------")

	case "create-admin":
		if len(os.Args) < 5 {
			log.Fatal("usage: create-admin <email> <password> <name>")
		}
		email := os.Args[2]
		password := os.Args[3]
		name := os.Args[4]

		_, err := userSvc.Create(ctx, user.CreateUserRequest{
			Name:     name,
			Email:    email,
			Password: password,
			Role:     "admin",
		})

		if err != nil {
			log.Fatalf("failed to create admin: %v", err)
		}
		fmt.Printf("Admin user '%s' created successfully.\n", email)

	case "register-client":
		if len(os.Args) < 5 {
			log.Fatal("usage: register-client <app_name> <platform> <password> [description]")
		}
		appName := os.Args[2]
		platform := os.Args[3]
		password := os.Args[4]
		desc := ""
		if len(os.Args) >= 6 {
			desc = os.Args[5]
		}

		// Verify admin password
		adminPassword := os.Getenv("ADMIN_PASSWORD")
		if adminPassword == "" {
			adminPassword = "nitip-admin-2026"
		}
		if password != adminPassword {
			log.Fatal("❌ Invalid admin password")
		}

		authSvc := auth.NewService(db)
		client, secret, err := authSvc.RegisterClient(ctx, appName, platform, desc)
		if err != nil {
			log.Fatalf("failed to register client: %v", err)
		}

		fmt.Println("\n  ╔══════════════════════════════════════════════════════════╗")
		fmt.Println("  ║            API Client Registered Successfully           ║")
		fmt.Println("  ╠══════════════════════════════════════════════════════════╣")
		fmt.Printf("  ║  App Name  : %-42s ║\n", client.AppName)
		fmt.Printf("  ║  Platform  : %-42s ║\n", client.Platform)
		fmt.Printf("  ║  API Key   : %-42s ║\n", client.ApiKey)
		fmt.Printf("  ║  API Secret: %-42s ║\n", secret)
		fmt.Println("  ╠══════════════════════════════════════════════════════════╣")
		fmt.Println("  ║  ⚠️  SAVE THE API SECRET NOW! It will NOT be shown again ║")
		fmt.Println("  ╚══════════════════════════════════════════════════════════╝")

	case "grant-token":
		if len(os.Args) < 4 {
			log.Fatal("usage: grant-token <app_name> <platform> [--jwt <email>]")
		}
		appName := os.Args[2]
		platform := os.Args[3]

		authSvc := auth.NewService(db)
		clients, err := authSvc.ListClients(ctx)
		if err != nil {
			log.Fatalf("failed to list clients: %v", err)
		}

		// Find matching client
		var targetClient *auth.ApiClient
		for _, c := range clients {
			if c.AppName == appName && c.Platform == platform && c.IsActive {
				targetClient = &c
				break
			}
		}
		if targetClient == nil {
			log.Fatalf("no active client found for app=%s platform=%s", appName, platform)
		}

		grant, err := authSvc.CreateGrantToken(ctx, targetClient.ID)
		if err != nil {
			log.Fatalf("failed to create grant token: %v", err)
		}

		fmt.Println("\n  ╔══════════════════════════════════════════════════════════╗")
		fmt.Println("  ║               Grant Token Generated                     ║")
		fmt.Println("  ╠══════════════════════════════════════════════════════════╣")
		fmt.Printf("  ║  Token     : %-42s ║\n", grant.Token)
		fmt.Printf("  ║  Expires   : %-42s ║\n", grant.ExpiresAt.Format("2006-01-02 15:04:05"))
		fmt.Println("  ╚══════════════════════════════════════════════════════════╝")

		// If --jwt flag is provided, also generate JWT directly
		if len(os.Args) >= 6 && os.Args[4] == "--jwt" {
			email := os.Args[5]
			u, err := userRepo.FindByEmail(ctx, email)
			if err != nil {
				log.Fatalf("user not found: %s", email)
			}

			jwtToken, err := jwtLib.GenerateToken(u.ID, u.Email, u.Role, u.IsVerified, "", u.TokenVersion)
			if err != nil {
				log.Fatalf("failed to generate JWT: %v", err)
			}

			refreshToken, err := jwtLib.GenerateRefreshToken(u.ID, "", u.TokenVersion)
			if err != nil {
				log.Fatalf("failed to generate refresh token: %v", err)
			}

			fmt.Println("\n  ╔══════════════════════════════════════════════════════════╗")
			fmt.Println("  ║               JWT Token (Direct)                        ║")
			fmt.Println("  ╠══════════════════════════════════════════════════════════╣")
			fmt.Printf("  ║  User      : %-42s ║\n", email)
			fmt.Printf("  ║  Role      : %-42s ║\n", u.Role)
			fmt.Printf("  ║  JWT       : %-42s ║\n", jwtToken[:40]+"...")
			fmt.Printf("  ║  Refresh   : %-42s ║\n", refreshToken[:40]+"...")
			fmt.Println("  ╠══════════════════════════════════════════════════════════╣")
			fmt.Println("  ║  Use: Authorization: Bearer <JWT>                       ║")
			fmt.Println("  ╚══════════════════════════════════════════════════════════╝")

			fmt.Printf("\nFull JWT:\n%s\n", jwtToken)
			fmt.Printf("\nFull Refresh Token:\n%s\n", refreshToken)
		} else {
			fmt.Println("\n  💡 Tip: Add --jwt <email> to also generate a JWT token directly")
			fmt.Println("  Example: make grant-token APP=nitip-mobile PLAT=android JWT=andi@nitip.id")
		}

	case "list-clients":
		authSvc := auth.NewService(db)
		clients, err := authSvc.ListClients(ctx)
		if err != nil {
			log.Fatalf("failed to list clients: %v", err)
		}
		fmt.Printf("\n--- Registered API Clients ---\n")
		for _, c := range clients {
			status := "✅ Active"
			if !c.IsActive {
				status = "❌ Inactive"
			}
			lastUsed := "Never"
			if c.LastUsedAt != nil {
				lastUsed = c.LastUsedAt.Format("2006-01-02 15:04:05")
			}
			fmt.Printf("  %s | %s (%s) | Key: %s... | Last Used: %s\n", status, c.AppName, c.Platform, c.ApiKey[:8], lastUsed)
		}
		fmt.Println("------------------------------")

	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: go run ./cmd/admin <command> [args]")
	fmt.Println("Commands:")
	fmt.Println("  get <key>                                    Get a config value")
	fmt.Println("  set <key> <value> [description]              Set a config value")
	fmt.Println("  list                                         List all dynamic configs")
	fmt.Println("  create-admin <email> <pwd> <name>            Create initial admin user")
	fmt.Println("  register-client <app> <platform> <pwd> [desc] Register API client (pwd from ADMIN_PASSWORD env)")
	fmt.Println("  list-clients                                 List all registered API clients")
	fmt.Println("  grant-token <app> <plat> [--jwt <email>]     Generate grant token (and optional JWT)")
}
