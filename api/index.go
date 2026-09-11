package handler

import (
	"net/http"

	app "github.com/Wysakm/wellness-travel-api"
)

// Handler is the Vercel Go runtime entrypoint. The application package owns
// initialization, routing, validation, and scoring; this adapter only bridges
// Vercel's request/response contract to the shared Gin router.
func Handler(w http.ResponseWriter, r *http.Request) {
	app.Handler(w, r)
}
