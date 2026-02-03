#!/usr/bin/env python3
"""
Tornado handlers per API REST OctoTracker Mini App

Endpoints:
- GET /api/rates/history - Storico tariffe per grafici
- GET /api/rates/current - Tariffe correnti
- GET /api/user/rates - Tariffe dell'utente autenticato
"""

import json
import logging
import os

from tornado.web import RequestHandler

from api.auth import TelegramAuthError, validate_init_data
from database import get_current_rates, get_latest_rate_date, get_rate_history, load_user

logger = logging.getLogger(__name__)

# Token del bot per validazione auth
BOT_TOKEN = os.getenv("TELEGRAM_BOT_TOKEN", "")

# Valori validi per validazione parametri
VALID_SERVIZI = {"luce", "gas"}
VALID_TIPI = {"fissa", "variabile"}
VALID_FASCE = {"monoraria", "bioraria", "trioraria"}


class BaseAPIHandler(RequestHandler):
    """Handler base con metodi comuni per API"""

    def set_default_headers(self):
        """Imposta headers comuni per tutte le risposte"""
        self.set_header("Content-Type", "application/json")
        # CORS per Mini App Telegram
        self.set_header("Access-Control-Allow-Origin", "*")
        self.set_header("Access-Control-Allow-Headers", "X-Telegram-Init-Data, Content-Type")
        self.set_header("Access-Control-Allow-Methods", "GET, OPTIONS")

    def options(self):
        """Handle preflight CORS requests"""
        self.set_status(204)
        self.finish()

    def write_json(self, data: dict, status: int = 200):
        """Helper per scrivere risposta JSON"""
        self.set_status(status)
        self.write(json.dumps(data))

    def write_error_json(self, error: str, status: int = 400):
        """Helper per scrivere errore JSON"""
        self.set_status(status)
        self.write(json.dumps({"success": False, "error": error}))

    def get_auth_data(self) -> dict | None:
        """
        Valida autenticazione Telegram e ritorna dati utente.

        Returns:
            Dict con auth data se valido, None se manca header

        Raises:
            TelegramAuthError: Se auth non valida
        """
        init_data = self.request.headers.get("X-Telegram-Init-Data")
        if not init_data:
            return None

        return validate_init_data(init_data, BOT_TOKEN)

    def get_user_id(self, auth_data: dict) -> str:
        """Estrae user_id da auth_data come stringa"""
        return str(auth_data["user"]["id"])


class RatesHistoryHandler(BaseAPIHandler):
    """
    GET /api/rates/history

    Query params:
    - servizio: "luce" o "gas" (required)
    - tipo: "fissa" o "variabile" (required)
    - fascia: "monoraria", "bioraria", "trioraria" (required)
    - days: numero giorni (default 365)
    """

    def get(self):
        try:
            # Verifica autenticazione
            auth_data = self.get_auth_data()
            if auth_data is None:
                self.write_error_json("Missing X-Telegram-Init-Data header", 401)
                return

            # Estrai e valida parametri
            servizio = self.get_argument("servizio", None)
            tipo = self.get_argument("tipo", None)
            fascia = self.get_argument("fascia", None)
            days_str = self.get_argument("days", "365")

            # Validazione servizio
            if not servizio:
                self.write_error_json("Missing required parameter: servizio", 400)
                return
            if servizio not in VALID_SERVIZI:
                self.write_error_json(
                    f"Invalid servizio: {servizio}. Must be one of: {VALID_SERVIZI}", 400
                )
                return

            # Validazione tipo
            if not tipo:
                self.write_error_json("Missing required parameter: tipo", 400)
                return
            if tipo not in VALID_TIPI:
                self.write_error_json(f"Invalid tipo: {tipo}. Must be one of: {VALID_TIPI}", 400)
                return

            # Validazione fascia
            if not fascia:
                self.write_error_json("Missing required parameter: fascia", 400)
                return
            if fascia not in VALID_FASCE:
                self.write_error_json(
                    f"Invalid fascia: {fascia}. Must be one of: {VALID_FASCE}", 400
                )
                return

            # Validazione days
            try:
                days = int(days_str)
                if days < 1:
                    days = 365
                if days > 3650:  # Max 10 anni
                    days = 3650
            except ValueError:
                days = 365

            # Recupera storico
            user_id = self.get_user_id(auth_data)
            logger.info(
                f"Rate history request: user={user_id}, {servizio}/{tipo}/{fascia}, days={days}"
            )

            history = get_rate_history(
                servizio=servizio,
                tipo=tipo,
                fascia=fascia,
                days=days,
                include_commercializzazione=True,
                include_stats=True,
            )

            self.write_json({"success": True, "data": history})

        except TelegramAuthError as e:
            logger.warning(f"Auth error: {e}")
            self.write_error_json(str(e), 401)

        except Exception as e:
            logger.error(f"Unexpected error in RatesHistoryHandler: {e}", exc_info=True)
            self.write_error_json("Internal server error", 500)


class RatesCurrentHandler(BaseAPIHandler):
    """
    GET /api/rates/current

    Ritorna le tariffe più recenti disponibili.
    """

    def get(self):
        try:
            # Verifica autenticazione
            auth_data = self.get_auth_data()
            if auth_data is None:
                self.write_error_json("Missing X-Telegram-Init-Data header", 401)
                return

            # Recupera tariffe correnti
            rates = get_current_rates()
            if rates is None:
                self.write_error_json("No rates found", 404)
                return

            # Recupera data
            rate_date = get_latest_rate_date()

            user_id = self.get_user_id(auth_data)
            logger.info(f"Current rates request: user={user_id}")

            self.write_json(
                {
                    "success": True,
                    "data": {
                        "date": rate_date,
                        **rates,
                    },
                }
            )

        except TelegramAuthError as e:
            logger.warning(f"Auth error: {e}")
            self.write_error_json(str(e), 401)

        except Exception as e:
            logger.error(f"Unexpected error in RatesCurrentHandler: {e}", exc_info=True)
            self.write_error_json("Internal server error", 500)


class UserRatesHandler(BaseAPIHandler):
    """
    GET /api/user/rates

    Ritorna le tariffe salvate dell'utente autenticato.
    """

    def get(self):
        try:
            # Verifica autenticazione
            auth_data = self.get_auth_data()
            if auth_data is None:
                self.write_error_json("Missing X-Telegram-Init-Data header", 401)
                return

            # Recupera dati utente
            user_id = self.get_user_id(auth_data)
            user_data = load_user(user_id)

            if user_data is None:
                self.write_error_json(f"User not found: {user_id}", 404)
                return

            logger.info(f"User rates request: user={user_id}")

            # Estrai solo le tariffe (non altri campi come last_notified_rates)
            response_data = {
                "luce": user_data.get("luce"),
                "gas": user_data.get("gas"),
            }

            self.write_json({"success": True, "data": response_data})

        except TelegramAuthError as e:
            logger.warning(f"Auth error: {e}")
            self.write_error_json(str(e), 401)

        except Exception as e:
            logger.error(f"Unexpected error in UserRatesHandler: {e}", exc_info=True)
            self.write_error_json("Internal server error", 500)
