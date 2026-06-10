package matching

import (
	"context"
	"log"
	"sort"
	"sync"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/order"
	"github.com/codecoffy/nitip-core/internal/domain/trip"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/notification"
	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/pkg/geolocation"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service interface {
	FindNearestRunners(ctx context.Context, lat, lng float64, radiusKm float64) ([]user.User, error)
	DispatchOrder(ctx context.Context, orderID string, runners []user.User) error

	// Worker Pool Methods
	EnqueueMatching(orderID uuid.UUID)
	StartWorkerPool(ctx context.Context, numWorkers int)
}

type rankedRunner struct {
	user  user.User
	score float64
}

type service struct {
	userRepo   user.Repository
	tripRepo   trip.Repository
	orderRepo  order.Repository
	redis      *cache.Redis
	fcm        notification.Notifier
	jobQueue   chan uuid.UUID
	workerOnce sync.Once
}

func NewService(userRepo user.Repository, tripRepo trip.Repository, orderRepo order.Repository, redis *cache.Redis, fcm notification.Notifier) Service {
	return &service{
		userRepo:  userRepo,
		tripRepo:  tripRepo,
		orderRepo: orderRepo,
		redis:     redis,
		fcm:       fcm,
		jobQueue:  make(chan uuid.UUID, 1000), // Buffered channel to prevent blocking
	}
}

func (s *service) EnqueueMatching(orderID uuid.UUID) {
	select {
	case s.jobQueue <- orderID:
		// Enqueued successfully
	default:
		log.Printf("Matching queue full, dropping order %s", orderID)
	}
}

func (s *service) StartWorkerPool(ctx context.Context, numWorkers int) {
	s.workerOnce.Do(func() {
		for i := 0; i < numWorkers; i++ {
			go s.worker(ctx, i)
		}
		log.Printf("Started %d matching workers", numWorkers)
	})
}

func (s *service) worker(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		case orderID := <-s.jobQueue:
			// Perform Matching Logic
			log.Printf("Worker %d processing matching for order %s", id, orderID)
			s.processMatching(ctx, orderID)
		}
	}
}

func (s *service) processMatching(ctx context.Context, orderID uuid.UUID) {
	ord, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		log.Printf("Error finding order %s: %v", orderID, err)
		return
	}

	// 1. Fetch active trips
	trips, err := s.tripRepo.FindActiveByLocation(ctx, ord.PickupLat, ord.PickupLng, 10.0)
	if err != nil {
		log.Printf("Error finding active trips: %v", err)
		return
	}

	// 2. Resolve N+1: Collect all RunnerIDs and fetch them in bulk
	runnerIDsMap := make(map[uuid.UUID]bool)
	for _, t := range trips {
		runnerIDsMap[t.RunnerID] = true
	}

	var runnerIDs []uuid.UUID
	for id := range runnerIDsMap {
		runnerIDs = append(runnerIDs, id)
	}

	runners, err := s.userRepo.FindByIDs(ctx, runnerIDs)
	if err != nil {
		log.Printf("Error fetching runners in bulk: %v", err)
		return
	}

	runnersMap := make(map[uuid.UUID]user.User)
	for _, r := range runners {
		runnersMap[r.ID] = r
	}

	var rankedRunners []rankedRunner
	maxDetourKm := 10.0 // Maximum extra distance allowance

	for _, t := range trips {
		runner, ok := runnersMap[t.RunnerID]
		if !ok {
			continue
		}

		// Calculate original journey distance for this trip (fixed plan)
		distOriginal := geolocation.Haversine(t.OriginLat, t.OriginLng, t.DestinationLat, t.DestinationLng)

		// Setup calculation origin (default to Trip Origin)
		calcOriginLat := t.OriginLat
		calcOriginLng := t.OriginLng

		// Removed for MVP v2 - Dynamic location updates are disabled, so we only use the static Trip Origin
		// if t.Status == trip.StatusStarted && runner.LastLat != nil && runner.LastLng != nil {
		// 	calcOriginLat = *runner.LastLat
		// 	calcOriginLng = *runner.LastLng
		// 	log.Printf("Trip %s is started, using Runner current location (%.4f, %.4f) as Origin", t.ID, calcOriginLat, calcOriginLng)
		// }

		// New Matching Logic: Detour Distance (Along the Way)
		// Path: Calculation Origin -> Order.Pickup -> Order.Delivery -> Trip.Destination
		distToPickup := geolocation.Haversine(calcOriginLat, calcOriginLng, ord.PickupLat, ord.PickupLng)
		distPickupToDelivery := geolocation.Haversine(ord.PickupLat, ord.PickupLng, ord.DeliveryLat, ord.DeliveryLng)
		distDeliveryToDest := geolocation.Haversine(ord.DeliveryLat, ord.DeliveryLng, t.DestinationLat, t.DestinationLng)

		distNewTotal := distToPickup + distPickupToDelivery + distDeliveryToDest
		extraDistance := distNewTotal - distOriginal

		// Capacity Check: Ensure Runner has enough weight and volume capacity
		if t.AvailableWeightKg < ord.WeightKg || t.AvailableVolumeLiters < ord.VolumeLiters {
			log.Printf("Trip %s capacity insufficient for Order %s (Weight: %.1f/%.1f, Vol: %.1f/%.1f)", 
				t.ID, orderID, ord.WeightKg, t.AvailableWeightKg, ord.VolumeLiters, t.AvailableVolumeLiters)
			continue
		}

		// Vehicle Type Restriction: Heavy or bulky items require roda 4 (Car/Pickup)
		// Threshold: > 20kg or > 50L
		if (ord.WeightKg > 20 || ord.VolumeLiters > 50) && t.VehicleType == "motorcycle" {
			log.Printf("Order %s too heavy/large for Runner %s vehicle: %s", orderID, runner.Name, t.VehicleType)
			continue
		}

		if extraDistance <= maxDetourKm {
			// Priority Matching: Calculate Score
			// Detour Score: 1.0 (best) to 0.0 (worst)
			detourScore := 1.0 - (extraDistance / maxDetourKm)
			// Trust Score: Normalized from user.TrustScore (0-100)
			trustScore := float64(runner.TrustScore) / 100.0

			// Weighted Total (Hardcoded weights as per MVP plan)
			totalScore := (0.6 * detourScore) + (0.4 * trustScore)

			log.Printf("Match found! Runner %s (Trust: %d) matches Order %s (Extra: %.2f km, Score: %.2f)", runner.Name, runner.TrustScore, orderID, extraDistance, totalScore)
			
			rankedRunners = append(rankedRunners, rankedRunner{
				user:  runner,
				score: totalScore,
			})
		}
	}

	if len(rankedRunners) > 0 {
		// Sort by Score DESC
		sort.Slice(rankedRunners, func(i, j int) bool {
			return rankedRunners[i].score > rankedRunners[j].score
		})

		// Limit to Top 5 runners to reduce noise
		maxNotify := 5
		if len(rankedRunners) > maxNotify {
			rankedRunners = rankedRunners[:maxNotify]
		}

		var matchedRunners []user.User
		for _, r := range rankedRunners {
			matchedRunners = append(matchedRunners, r.user)
		}

		if err := s.DispatchOrder(ctx, orderID.String(), matchedRunners); err != nil {
			log.Printf("Error dispatching matched order %s: %v", orderID, err)
		}
	}
}

func (s *service) FindNearestRunners(ctx context.Context, lat, lng float64, radiusKm float64) ([]user.User, error) {
	// If Redis is not available, fallback to efficient database spatial search
	if s.redis == nil {
		return s.FindNearestRunnersManual(ctx, lat, lng, radiusKm)
	}

	// Use Redis GEOSEARCH for high performance
	results, err := s.redis.Client().GeoSearch(ctx, "runners_live", &redis.GeoSearchQuery{
		Longitude:  lng,
		Latitude:   lat,
		Radius:     radiusKm,
		RadiusUnit: "km",
	}).Result()

	if err != nil {
		log.Printf("Redis GEOSEARCH failed: %v, falling back to database", err)
		return s.FindNearestRunnersManual(ctx, lat, lng, radiusKm)
	}

	var nearbyRunners []user.User
	for _, idStr := range results {
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		u, err := s.userRepo.FindByID(ctx, id)
		if err == nil && !u.IsSuspended {
			nearbyRunners = append(nearbyRunners, *u)
		}
	}

	return nearbyRunners, nil
}

// FindNearestRunnersManual uses an efficient Bounding Box query in DB
func (s *service) FindNearestRunnersManual(ctx context.Context, lat, lng float64, radiusKm float64) ([]user.User, error) {
	nearbyUsers, err := s.userRepo.FindNearbyRunners(ctx, lat, lng, radiusKm)
	if err != nil {
		return nil, err
	}

	var nearbyRunners []user.User
	for _, u := range nearbyUsers {
		// Even with Bounding Box, we refine with exact Haversine calculation in Go
		if u.LastLat != nil && u.LastLng != nil {
			distance := geolocation.Haversine(lat, lng, *u.LastLat, *u.LastLng)
			if distance <= radiusKm {
				nearbyRunners = append(nearbyRunners, u)
			}
		}
	}

	return nearbyRunners, nil
}

func (s *service) DispatchOrder(ctx context.Context, orderID string, runners []user.User) error {
	if s.fcm == nil || !config.App.FcmEnabled {
		return nil
	}

	var tokens []string
	for _, r := range runners {
		if r.FcmToken != nil && *r.FcmToken != "" {
			tokens = append(tokens, *r.FcmToken)
		}
	}

	if len(tokens) == 0 {
		return nil
	}

	err := s.fcm.SendMulticast(ctx, tokens, "Order Baru di Sekitarmu!", "Ada penitip yang membutuhkan bantuanmu.", map[string]string{
		"order_id": orderID,
	})

	return err
}
