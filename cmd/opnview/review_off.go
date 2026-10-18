//go:build !review

package main

import "net/http"

// reviewHandler is the server itself: a build without the review tag has no review mode, no flag for
// it and no reference to the recorded answers (review.go).
func reviewHandler(server http.Handler) (http.Handler, error) {
	return server, nil
}
