// Package api porta api/auth.py e api/handlers.py: la validazione di
// initData delle Telegram Mini App e gli endpoint REST usati da
// webapp/js/app.js.
//
// # Autenticazione
//
// ValidateInitData verifica la firma HMAC-SHA256 di initData (chiave
// segreta HMAC-SHA256("WebAppData", bot_token)), l'età di auth_date
// (DefaultMaxAgeSeconds = 24 ore) e la presenza di user.id, restituendo un
// *TelegramAuthError con gli stessi messaggi di Python.
//
// # Endpoint
//
//	a := api.New(store, cfg.BotToken)  // store: *database.Store
//	a.Register(mux)                    // /api/rates/history, /api/rates/current, /api/user/rates
//
// Le risposte replicano quelle di Tornado (package tornado): stesso JSON di
// json.dumps (chiavi, ordine, null, float come "72.0", escape \uXXXX),
// status, header CORS, OPTIONS → 204, altri metodi → 405 HTML, ETag/304.
package api
