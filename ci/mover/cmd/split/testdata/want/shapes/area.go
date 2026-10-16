// Package shapes measures shapes.
package shapes

import "example.com/fixture/shapes/geometry"

// Area is the area of a square of a side.
func Area(side int) int { return geometry.Square(side) }

// Perimeter is the perimeter of a square of a side.
func Perimeter(side int) int { return geometry.Sides * side }
