package model

import "math"

// EarthRadiusM is the mean Earth radius used for local planar projection.
const EarthRadiusM = 6_371_000.0

// BBox is a latitude/longitude rectangle (WGS-84, degrees).
type BBox struct {
	MinLat float64 `json:"min_lat" form:"min_lat"`
	MaxLat float64 `json:"max_lat" form:"max_lat"`
	MinLon float64 `json:"min_lon" form:"min_lon"`
	MaxLon float64 `json:"max_lon" form:"max_lon"`
}

// Valid reports whether the rectangle is well-formed.
func (b BBox) Valid() bool {
	return b.MinLat >= -90 && b.MaxLat <= 90 && b.MinLat <= b.MaxLat &&
		b.MinLon >= -180 && b.MaxLon <= 180 && b.MinLon <= b.MaxLon
}

// ContainsPoint reports whether a point lies inside the rectangle.
func (b BBox) ContainsPoint(lat, lon float64) bool {
	return lat >= b.MinLat && lat <= b.MaxLat && lon >= b.MinLon && lon <= b.MaxLon
}

// Intersects reports whether the two bounding boxes overlap (closed intervals).
func (b BBox) Intersects(o BBox) bool {
	return b.MinLat <= o.MaxLat && b.MaxLat >= o.MinLat &&
		b.MinLon <= o.MaxLon && b.MaxLon >= o.MinLon
}

// BinGeoPoint projects a range/bearing offset (metres, degrees true north)
// from a reference ship position to lat/lon using an equirectangular
// approximation, accurate enough at radar-slice scale (tens of km).
func BinGeoPoint(shipLat, shipLon, bearingDeg, rangeM float64) (lat, lon float64) {
	bearing := bearingDeg * math.Pi / 180
	north := rangeM * math.Cos(bearing)
	east := rangeM * math.Sin(bearing)
	lat = shipLat + north/EarthRadiusM*180/math.Pi
	lon = shipLon + east/(EarthRadiusM*math.Cos(shipLat*math.Pi/180))*180/math.Pi
	return lat, lon
}
