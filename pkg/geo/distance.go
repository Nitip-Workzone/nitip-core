package geo

import (
	"math"
)

// Haversine calculates the distance between two points on the Earth's surface in Kilometers.
func Haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371 // Earth radius in Kilometers

	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLon := (lon2 - lon1) * math.Pi / 180.0

	lat1 = lat1 * math.Pi / 180.0
	lat2 = lat2 * math.Pi / 180.0

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Sin(dLon/2)*math.Sin(dLon/2)*math.Cos(lat1)*math.Cos(lat2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return R * c
}

// CalculateETA returns estimated minutes based on distance and average speed (km/h)
func CalculateETA(distanceKm float64, avgSpeedKmh float64) int {
	if avgSpeedKmh <= 0 {
		avgSpeedKmh = 30 // Default 30 km/h
	}
	hours := distanceKm / avgSpeedKmh
	return int(math.Ceil(hours * 60))
}
