# CLAUDE.md — OctoTracker

Bot Telegram che confronta ogni giorno le tariffe luce/gas dell'utente con
le offerte Octopus Energy pubblicate negli open data ARERA e avvisa quando
conviene cambiare. Include una Mini App Telegram con lo storico tariffe.

Fino alla 4.x il bot era scritto in Python; dalla 5.0 è in Go. Il
comportamento verso gli utenti (testi, tastiere, flussi) e il formato del
database sono rimasti identici: le golden in `testdata/` lo verificano.

## Stack e comandi

Go (versione in `go.mod`), `CGO_ENABLED=0`. Dipendenze: `gotgbot/v2` (Bot
API e conversation handler) e `modernc.org/sqlite` (SQLite puro Go). Tutto
il resto è stdlib. Docker distroless, multi-arch (amd64, arm64).

```bash
go test -race ./...
go vet ./... && gofmt -l cmd internal webapp
GOTOOLCHAIN=$(go env GOVERSION) go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
GOTOOLCHAIN=$(go env GOVERSION) go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
go test -race -coverpkg=./internal/... -coverprofile=coverage.out ./... \
  && go tool cover -func=coverage.out | tail -1      # gate CI: ≥ 80%
go run ./cmd/octotracker help                         # sottocomandi
```

Gli strumenti vanno compilati con la stessa Go del progetto: una versione
più vecchia non legge l'export data di Go 1.27. `$(go env GOVERSION)`,
dentro il modulo, è quella di `go.mod`.

## Sottocomandi del binario

`serve` (default) avvia bot, webhook, scheduler e server HTTP. Gli altri
eseguono un solo pezzo e terminano: `check`, `remind`, `scrape`,
`broadcast [message_file] [users_file]`, `backfill [--days N] [--dry-run]
[--delay S] [--log-level L]`, `healthcheck`, `version`.

L'immagine non ha shell: in produzione si lanciano con
`docker exec octotracker /app/octotracker <comando>`.

## Mappa

`cmd/octotracker` sottocomandi · `internal/app` dispatcher, webhook,
scheduler, server HTTP, error handler · `internal/config` env ·
`internal/logging` formato log e rotazione del file ·
`internal/database` SQLite, modelli, migrazioni · `internal/scraper`
download e parsing XML ARERA, backfill · `internal/checker` confronto
tariffe e notifiche · `internal/reminder` scadenze offerte fisse ·
`internal/handlers/{registration,commands,feedback,rateupdate}`
conversazioni e comandi · `internal/api` endpoint della Mini App ·
`internal/health` `/health` · `internal/broadcast` invio massivo ·
`internal/formatters`, `internal/dateutil` testi, numeri, date ·
`internal/tg` filtri e helper Telegram · `internal/testutil` client
Telegram finto · `webapp/` Mini App (embeddata nel binario) ·
`static/` landing per Cloudflare Pages.

## Regole sempre attive

1. **Branch separato + PR, sempre.** Mai commit o push su `main`. Il merge
   lo fa l'utente.
2. **Test con ogni modifica.** Ogni package con logica ha i suoi test;
   coverage di `internal/` ≥ 80% (gate in CI). Prima del commit:
   gofmt, vet, staticcheck, `go test -race ./...`.
3. **Testi utente e formato dati sono un contratto.** Messaggi Telegram,
   `callback_data`, colonne SQLite e JSON di `last_notified_rates` /
   `pending_rates` sono letti da utenti e righe già in produzione:
   cambiarli è una decisione, non un dettaglio. Il JSON delle API è letto
   da `webapp/js/app.js`.
4. **Le golden sono la fonte di verità del comportamento.** I file in
   `*/testdata/*_golden.json` sono stati generati eseguendo la versione
   Python (commit `606e4c5`, ultima versione Python su `main`). Coprono
   solo il contratto della regola 3 più Mini App API, `/health`, webhook,
   scheduler, parsing ARERA e backfill. Il port fedele, che emulava anche
   CPython, Tornado e python-telegram-bot, è il commit `0f051f6`: quell'
   emulazione è stata tolta e non va reintrodotta. Testi di log, errori
   HTTP di Tornado e parsing Python di input esotici (cifre Unicode,
   underscore nei numeri) non sono contratto. Per cambiare un testo di
   proposito: modifica il codice, aggiorna la golden nello stesso commit e
   spiega il motivo nella PR.
5. **Prima la stdlib.** Una dipendenza nuova va motivata nella PR.
6. **Mai nei log** token del bot, testo dei feedback, codici offerta.
   `user_id` sì.

## Logging

Ogni package dichiara `var logger = logging.Logger("<nome>")` e logga con
`fmt.Sprintf` nel formato storico
`YYYY-MM-DD HH:MM:SS - nome - LIVELLO - messaggio`. In `serve` i log vanno
su stdout e su `data/octotracker.log` (rotazione giornaliera, 3 giorni).

## Mini App (Telegram WebApp)

File in `webapp/` (`index.html`, `css/style.css`, `js/app.js`), inclusi nel
binario con `go:embed` e serviti su `HEALTH_PORT` sotto `/app/`.

**Cache busting**: quando modifichi CSS o JavaScript incrementa la versione
negli include di `webapp/index.html`, altrimenti Telegram e i browser
continuano a usare i file vecchi:

```html
<link rel="stylesheet" href="css/style.css?v=3">  <!-- Incrementa v=X -->
<script src="js/app.js?v=3"></script>              <!-- Incrementa v=X -->
```

API (porta 8444, header `X-Telegram-Init-Data` obbligatorio):
`GET /api/rates/history`, `GET /api/rates/current`, `GET /api/user/rates`.
`WEBAPP_URL` è l'URL pubblico della Mini App.

## Docker

- Build multi-stage: lo stage Go fa cross-compile per l'architettura di
  destinazione (niente QEMU); il runtime è `distroless/static`.
- Il container gira come root perché il volume `./data` di produzione è
  stato scritto da root. Per passare a `nonroot` serve prima
  `chown -R 65532:65532 data` sull'host.
- `HEALTHCHECK` usa `octotracker healthcheck` (niente curl nell'immagine).
- La CI controlla che l'immagine amd64 resti sotto i 30 MB.

## CI/CD

`.github/workflows/ci.yml`: gofmt, vet, staticcheck, govulncheck,
`go test -race` con gate di coverage, SonarCloud, build Docker multi-arch.
`release-docker.yml`: una release pubblicata (tag `vX.Y.Z`) pubblica
l'immagine su GHCR. Non bypassare mai i check.
