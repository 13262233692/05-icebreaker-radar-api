package model

import "time"

// IceFilter selects ice records by time range and geographic rectangle.
type IceFilter struct {
	From time.Time
	To   time.Time
	BBox BBox
	// HasBBox enables spatial filtering when all four edges are provided.
	HasBBox bool
	Limit   int
}
