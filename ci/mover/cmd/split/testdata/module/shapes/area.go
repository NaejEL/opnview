// Package shapes measures shapes.
package shapes

// Area is the area of a square of a side.
func Area(side int) int { return square(side) }

// Perimeter is the perimeter of a square of a side.
func Perimeter(side int) int { return sides * side }
