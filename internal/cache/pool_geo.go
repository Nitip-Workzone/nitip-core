package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	GeoKeyRunnersLive = "runners:live"  // GEOADD
	GeoKeyPoolPrefix  = "pool:cell:"    // SETEX JSON snapshot
	CounterPrefix     = "pool:counter:" // INCR counters
)

// Pool cell snapshot TTL 5s, matches vision doc
const PoolCellTTL = 5 * time.Second

// GeoAddRunnerLive adds runner location to Redis GEO set
func (r *Redis) GeoAddRunnerLive(ctx context.Context, runnerID string, lat, lng float64) error {
	return r.client.GeoAdd(ctx, GeoKeyRunnersLive, &redis.GeoLocation{
		Name:      runnerID,
		Latitude:  lat,
		Longitude: lng,
	}).Err()
}

// GeoRadiusRunners finds runners within radius (meters) from point
func (r *Redis) GeoRadiusRunners(ctx context.Context, lat, lng float64, radiusM float64) ([]redis.GeoLocation, error) {
	return r.client.GeoSearchLocation(ctx, GeoKeyRunnersLive, &redis.GeoSearchLocationQuery{
		GeoSearchQuery: redis.GeoSearchQuery{
			Longitude:  lng,
			Latitude:   lat,
			Radius:     radiusM,
			RadiusUnit: "m",
			Sort:       "ASC",
			Count:      50,
		},
		WithDist: true,
	}).Result()
}

// Pool counters (no Grafana - use Redis INCR)

func (r *Redis) IncrCounter(ctx context.Context, name string) (int64, error) {
	key := CounterPrefix + name
	val, err := r.client.Incr(ctx, key).Result()
	if err == nil {
		// set expiry 24h on first incr
		r.client.Expire(ctx, key, 24*time.Hour)
	}
	return val, err
}

func (r *Redis) GetCounter(ctx context.Context, name string) (int64, error) {
	key := CounterPrefix + name
	val, err := r.client.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return val, err
}

func (r *Redis) GetAllPoolCounters(ctx context.Context) (map[string]int64, error) {
	// Known counters
	names := []string{
		"events:total",
		"sse:connections",
		"sse:peak",
		"claim:conflict",
		"claim:success",
		"fcm:sent",
		"fcm:failed",
		"orders:created",
		"orders:claimed",
	}
	result := make(map[string]int64)
	for _, n := range names {
		v, _ := r.GetCounter(ctx, n)
		result[n] = v
	}
	return result, nil
}

// Pool cell cache - SETEX JSON snapshot per geohash cell TTL 5s

func (r *Redis) SetPoolSnapshot(ctx context.Context, cellKey string, data interface{}) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	key := GeoKeyPoolPrefix + cellKey
	return r.client.Set(ctx, key, b, PoolCellTTL).Err()
}

func (r *Redis) GetPoolSnapshot(ctx context.Context, cellKey string) (string, error) {
	key := GeoKeyPoolPrefix + cellKey
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	return val, err
}

// Set runner location + update counters helper
func (r *Redis) TrackRunnerLocation(ctx context.Context, runnerID string, lat, lng float64) error {
	if err := r.GeoAddRunnerLive(ctx, runnerID, lat, lng); err != nil {
		return err
	}
	// Also store latest lat/lng as hash for quick lookup
	hKey := fmt.Sprintf("runner:loc:%s", runnerID)
	return r.client.HSet(ctx, hKey, map[string]interface{}{
		"lat": lat,
		"lng": lng,
		"ts":  time.Now().Unix(),
	}).Err()
}
