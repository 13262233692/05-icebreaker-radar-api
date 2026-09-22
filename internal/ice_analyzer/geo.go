package ice_analyzer

import "math"

const earthRadiusM = 6371000.0

// destinationPoint returns the WGS-84 lat/lon reached by travelling distance
// meters on the given true bearing from the origin.
func destinationPoint(lat, lon, bearingDeg, distanceM float64) (float64, float64) {
	lat1 := lat * math.Pi / 180
	lon1 := lon * math.Pi / 180
	brg := bearingDeg * math.Pi / 180
	d := distanceM / earthRadiusM

	lat2 := math.Asin(math.Sin(lat1)*math.Cos(d) +
		math.Cos(lat1)*math.Sin(d)*math.Cos(brg))
	lon2 := lon1 + math.Atan2(
		math.Sin(brg)*math.Sin(d)*math.Cos(lat1),
		math.Cos(d)-math.Sin(lat1)*math.Sin(lat2),
	)
	return lat2 * 180 / math.Pi, lon2 * 180 / math.Pi
}
