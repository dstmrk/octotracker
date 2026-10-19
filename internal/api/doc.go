// Package api contiene la validazione di initData delle Telegram Mini App
// e gli endpoint REST usati da webapp/js/app.js.
//
// # Autenticazione
//
// ValidateInitData verifica la firma HMAC-SHA256 di initData (chiave
// segreta HMAC-SHA256("WebAppData", bot_token)), l'età di auth_date
// (DefaultMaxAgeSeconds = 24 ore) e la presenza di user.id, restituendo un
// *TelegramAuthError il cui messaggio finisce nella risposta 401.
//
// # Endpoint
//
//	a := api.New(store, cfg.BotToken)  // store: *database.Store
//	a.Register(mux)                    // /api/rates/history, /api/rates/current, /api/user/rates
//
// Ogni risposta ha gli header CORS; GET risponde con JSON
// {"success": true, "data": ...} oppure {"success": false, "error": ...},
// OPTIONS con 204 e ogni altro metodo con 405.
package api
