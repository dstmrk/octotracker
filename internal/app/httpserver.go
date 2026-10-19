package app

import (
	"io/fs"
	"net/http"

	"github.com/dstmrk/octotracker/internal/api"
	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/health"
)

// AppPrefix è il prefisso della Mini App.
const AppPrefix = "/app/"

// NewHTTPHandler crea il server HTTP su HEALTH_PORT:
//
//   - /health              → health check
//   - /api/rates/history   → storico tariffe (Mini App)
//   - /api/rates/current   → tariffe correnti (Mini App)
//   - /api/user/rates      → tariffe dell'utente (Mini App)
//   - /app/...             → file statici della Mini App (GET e HEAD;
//     /app/ è index.html)
//
// Ogni altro percorso riceve 404.
func NewHTTPHandler(store *database.Store, tasks health.TaskStatus, botToken string, webapp fs.FS) http.Handler {
	a := api.New(store, botToken)
	mux := http.NewServeMux()
	mux.Handle(health.Path, health.NewHandler(store, tasks))
	mux.Handle(api.PathRatesHistory, a.RatesHistoryHandler())
	mux.Handle(api.PathRatesCurrent, a.RatesCurrentHandler())
	mux.Handle(api.PathUserRates, a.UserRatesHandler())
	mux.Handle(http.MethodGet+" "+AppPrefix, http.StripPrefix(AppPrefix, http.FileServerFS(webapp)))
	return mux
}
