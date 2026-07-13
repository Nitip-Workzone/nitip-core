package trip

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

type CreateTripRequest struct {
	OriginName          string     `json:"origin_name"      validate:"required"`
	OriginLat           float64    `json:"origin_lat"       validate:"required"`
	OriginLng           float64    `json:"origin_lng"       validate:"required"`
	DestinationName     string     `json:"destination_name" validate:"required"`
	DestinationLat      float64    `json:"destination_lat"  validate:"required"`
	DestinationLng      float64    `json:"destination_lng"  validate:"required"`
	DepartureTime       time.Time  `json:"departure_time"   validate:"required"`
	ReturnTime          *time.Time `json:"return_time"`
	Notes               string     `json:"notes"`
	VehicleType         string     `json:"vehicle_type"     validate:"required,oneof=motorcycle car pickup"`
	MaxWeightKg         float64    `json:"max_weight_kg"`
	MaxVolumeLiters     float64    `json:"max_volume_liters"`
	AllowedServiceTypes []string   `json:"allowed_service_types"`
	IsRoundTrip         bool       `json:"is_round_trip"`
}

type Service interface {
	Create(ctx context.Context, runnerID uuid.UUID, req CreateTripRequest) (*Trip, error)
	GetByRunner(ctx context.Context, runnerID uuid.UUID) ([]Trip, error)
	Start(ctx context.Context, tripID, runnerID uuid.UUID) error
	Cancel(ctx context.Context, tripID, runnerID uuid.UUID) error
	Complete(ctx context.Context, tripID, runnerID uuid.UUID) error
	ListActive(ctx context.Context) ([]Trip, error)
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func (s *service) Create(ctx context.Context, runnerID uuid.UUID, req CreateTripRequest) (*Trip, error) {
	// Set defaults based on vehicle type if not explicitly provided
	maxWeight := req.MaxWeightKg
	maxVolume := req.MaxVolumeLiters

	if maxWeight <= 0 || maxVolume <= 0 {
		switch req.VehicleType {
		case "motorcycle":
			if maxWeight <= 0 {
				maxWeight = 20
			}
			if maxVolume <= 0 {
				maxVolume = 40
			}
		case "car":
			if maxWeight <= 0 {
				maxWeight = 200
			}
			if maxVolume <= 0 {
				maxVolume = 400
			}
		case "pickup":
			if maxWeight <= 0 {
				maxWeight = 1000
			}
			if maxVolume <= 0 {
				maxVolume = 2000
			}
		}
	}

	trip := &Trip{
		ID:                    uuid.New(),
		RunnerID:              runnerID,
		OriginName:            req.OriginName,
		OriginLat:             req.OriginLat,
		OriginLng:             req.OriginLng,
		DestinationName:       req.DestinationName,
		DestinationLat:        req.DestinationLat,
		DestinationLng:        req.DestinationLng,
		DepartureTime:         req.DepartureTime,
		ReturnTime:            req.ReturnTime,
		Status:                StatusActive,
		Notes:                 req.Notes,
		VehicleType:           req.VehicleType,
		MaxWeightKg:           maxWeight,
		AvailableWeightKg:     maxWeight,
		MaxVolumeLiters:       maxVolume,
		AvailableVolumeLiters: maxVolume,
		AllowedServiceTypes:   req.AllowedServiceTypes,
		IsRoundTrip:           req.IsRoundTrip,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	if len(trip.AllowedServiceTypes) == 0 {
		trip.AllowedServiceTypes = []string{"instant", "regular"}
	}

	if err := s.repo.Create(ctx, trip); err != nil {
		return nil, err
	}

	return trip, nil
}

func (s *service) GetByRunner(ctx context.Context, runnerID uuid.UUID) ([]Trip, error) {
	return s.repo.FindByRunnerID(ctx, runnerID)
}

func (s *service) ListActive(ctx context.Context) ([]Trip, error) {
	return s.repo.FindAllActive(ctx)
}

func (s *service) Start(ctx context.Context, tripID, runnerID uuid.UUID) error {
	trip, err := s.repo.FindByID(ctx, tripID)
	if err != nil {
		return err
	}

	if trip.RunnerID != runnerID {
		return nil // Unauthorized or not found
	}

	trip.Status = StatusStarted
	trip.UpdatedAt = time.Now()

	return s.repo.Update(ctx, trip)
}

func (s *service) Cancel(ctx context.Context, tripID, runnerID uuid.UUID) error {
	trip, err := s.repo.FindByID(ctx, tripID)
	if err != nil {
		return err
	}

	if trip.RunnerID != runnerID {
		return nil // Unauthorized or not found
	}

	trip.Status = StatusCancelled
	trip.UpdatedAt = time.Now()

	return s.repo.Update(ctx, trip)
}

func (s *service) Complete(ctx context.Context, tripID, runnerID uuid.UUID) error {
	trip, err := s.repo.FindByID(ctx, tripID)
	if err != nil {
		return err
	}

	if trip.RunnerID != runnerID {
		return errors.New("perjalanan tidak ditemukan atau Anda tidak memiliki akses")
	}

	if trip.Status != StatusStarted {
		return errors.New("tidak dapat menyelesaikan perjalanan yang belum dimulai")
	}

	trip.Status = StatusCompleted
	trip.UpdatedAt = time.Now()

	return s.repo.Update(ctx, trip)
}
