#!/usr/bin/env python3
"""
Test per api/handlers.py - Tornado handlers per API REST

Testa gli endpoint:
- GET /api/rates/history
- GET /api/rates/current
- GET /api/user/rates
"""

import json
from unittest.mock import MagicMock, Mock, patch

from api.handlers import (
    RatesCurrentHandler,
    RatesHistoryHandler,
    UserRatesHandler,
)


# Helper per generare initData valido nei test
def mock_valid_auth(user_id: int = 123456789):
    """Ritorna mock auth data come se fosse validato"""
    return {
        "user": {
            "id": user_id,
            "first_name": "Mario",
            "last_name": "Rossi",
            "username": "mariorossi",
        },
        "auth_date": "1234567890",
    }


class TestRatesHistoryHandler:
    """Test per RatesHistoryHandler - GET /api/rates/history"""

    def create_handler(self, query_args=None, headers=None):
        """Helper per creare handler con mock"""
        handler = RatesHistoryHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()

        # Mock query arguments
        query_args = query_args or {}
        handler.get_argument = Mock(side_effect=lambda k, default=None: query_args.get(k, default))

        # Mock headers
        headers = headers or {}
        handler.request.headers = headers

        return handler

    def test_get_success(self):
        """Test GET con parametri validi"""
        handler = self.create_handler(
            query_args={
                "servizio": "luce",
                "tipo": "fissa",
                "fascia": "monoraria",
                "days": "30",
            },
            headers={"X-Telegram-Init-Data": "valid_init_data"},
        )

        with (
            patch("api.handlers.validate_init_data") as mock_auth,
            patch("api.handlers.get_rate_history") as mock_history,
        ):
            mock_auth.return_value = mock_valid_auth()
            mock_history.return_value = {
                "labels": ["2025-01-01", "2025-01-02"],
                "data": [0.115, 0.116],
                "period": {"from": "2025-01-01", "to": "2025-01-02"},
            }

            handler.get()

            # Verifica status 200
            handler.set_status.assert_called_with(200)

            # Verifica response JSON
            handler.write.assert_called_once()
            response = json.loads(handler.write.call_args[0][0])
            assert response["success"] is True
            assert "data" in response
            assert response["data"]["labels"] == ["2025-01-01", "2025-01-02"]

    def test_get_missing_auth_header(self):
        """Test GET senza header di autenticazione"""
        handler = self.create_handler(
            query_args={"servizio": "luce", "tipo": "fissa", "fascia": "monoraria"},
            headers={},  # Nessun header auth
        )

        handler.get()

        # Deve ritornare 401 Unauthorized
        handler.set_status.assert_called_with(401)
        response = json.loads(handler.write.call_args[0][0])
        assert response["success"] is False
        assert "error" in response

    def test_get_invalid_auth(self):
        """Test GET con autenticazione non valida"""
        handler = self.create_handler(
            query_args={"servizio": "luce", "tipo": "fissa", "fascia": "monoraria"},
            headers={"X-Telegram-Init-Data": "invalid_data"},
        )

        with patch("api.handlers.validate_init_data") as mock_auth:
            from api.auth import TelegramAuthError

            mock_auth.side_effect = TelegramAuthError("Invalid signature")

            handler.get()

            handler.set_status.assert_called_with(401)

    def test_get_missing_servizio(self):
        """Test GET senza parametro servizio"""
        handler = self.create_handler(
            query_args={"tipo": "fissa", "fascia": "monoraria"},
            headers={"X-Telegram-Init-Data": "valid"},
        )

        with patch("api.handlers.validate_init_data") as mock_auth:
            mock_auth.return_value = mock_valid_auth()

            handler.get()

            # Deve ritornare 400 Bad Request
            handler.set_status.assert_called_with(400)
            response = json.loads(handler.write.call_args[0][0])
            assert "servizio" in response["error"].lower()

    def test_get_invalid_servizio(self):
        """Test GET con servizio non valido"""
        handler = self.create_handler(
            query_args={"servizio": "acqua", "tipo": "fissa", "fascia": "monoraria"},
            headers={"X-Telegram-Init-Data": "valid"},
        )

        with patch("api.handlers.validate_init_data") as mock_auth:
            mock_auth.return_value = mock_valid_auth()

            handler.get()

            handler.set_status.assert_called_with(400)
            response = json.loads(handler.write.call_args[0][0])
            assert "servizio" in response["error"].lower()

    def test_get_default_days_365(self):
        """Test GET usa default 365 giorni"""
        handler = self.create_handler(
            query_args={
                "servizio": "luce",
                "tipo": "fissa",
                "fascia": "monoraria",
                # days non specificato
            },
            headers={"X-Telegram-Init-Data": "valid"},
        )

        with (
            patch("api.handlers.validate_init_data") as mock_auth,
            patch("api.handlers.get_rate_history") as mock_history,
        ):
            mock_auth.return_value = mock_valid_auth()
            mock_history.return_value = {"labels": [], "data": [], "period": {}}

            handler.get()

            # Verifica che get_rate_history sia chiamato con days=365
            mock_history.assert_called_once()
            call_kwargs = mock_history.call_args
            assert call_kwargs[1].get("days") == 365 or call_kwargs[0][3] == 365

    def test_get_custom_days(self):
        """Test GET con days personalizzato"""
        handler = self.create_handler(
            query_args={
                "servizio": "luce",
                "tipo": "fissa",
                "fascia": "monoraria",
                "days": "90",
            },
            headers={"X-Telegram-Init-Data": "valid"},
        )

        with (
            patch("api.handlers.validate_init_data") as mock_auth,
            patch("api.handlers.get_rate_history") as mock_history,
        ):
            mock_auth.return_value = mock_valid_auth()
            mock_history.return_value = {"labels": [], "data": [], "period": {}}

            handler.get()

            # Verifica days=90
            call_args = mock_history.call_args
            # Controlla sia args posizionali che kwargs
            assert 90 in call_args[0] or call_args[1].get("days") == 90

    def test_get_cors_headers(self):
        """Test che i CORS headers sono impostati via set_default_headers"""
        handler = self.create_handler(
            query_args={"servizio": "luce", "tipo": "fissa", "fascia": "monoraria"},
            headers={"X-Telegram-Init-Data": "valid"},
        )

        # Chiama set_default_headers esplicitamente (Tornado lo fa automaticamente)
        handler.set_default_headers()

        # Verifica Content-Type JSON
        set_header_calls = handler.set_header.call_args_list
        content_type_set = any(
            call[0][0] == "Content-Type" and "application/json" in call[0][1]
            for call in set_header_calls
        )
        assert content_type_set

        # Verifica CORS header
        cors_set = any(call[0][0] == "Access-Control-Allow-Origin" for call in set_header_calls)
        assert cors_set


class TestRatesCurrentHandler:
    """Test per RatesCurrentHandler - GET /api/rates/current"""

    def create_handler(self, headers=None):
        """Helper per creare handler con mock"""
        handler = RatesCurrentHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()
        handler.request.headers = headers or {}
        return handler

    def test_get_success(self):
        """Test GET tariffe correnti"""
        handler = self.create_handler(
            headers={"X-Telegram-Init-Data": "valid"},
        )

        with (
            patch("api.handlers.validate_init_data") as mock_auth,
            patch("api.handlers.get_current_rates") as mock_rates,
            patch("api.handlers.get_latest_rate_date") as mock_date,
        ):
            mock_auth.return_value = mock_valid_auth()
            mock_rates.return_value = {
                "luce": {"fissa": {"monoraria": {"energia": 0.115}}},
                "gas": {"fissa": {"monoraria": {"energia": 0.39}}},
            }
            mock_date.return_value = "2025-01-15"

            handler.get()

            handler.set_status.assert_called_with(200)
            response = json.loads(handler.write.call_args[0][0])
            assert response["success"] is True
            assert response["data"]["date"] == "2025-01-15"
            assert "luce" in response["data"]

    def test_get_no_rates(self):
        """Test GET quando non ci sono tariffe"""
        handler = self.create_handler(
            headers={"X-Telegram-Init-Data": "valid"},
        )

        with (
            patch("api.handlers.validate_init_data") as mock_auth,
            patch("api.handlers.get_current_rates") as mock_rates,
        ):
            mock_auth.return_value = mock_valid_auth()
            mock_rates.return_value = None

            handler.get()

            handler.set_status.assert_called_with(404)
            response = json.loads(handler.write.call_args[0][0])
            assert response["success"] is False


class TestUserRatesHandler:
    """Test per UserRatesHandler - GET /api/user/rates"""

    def create_handler(self, headers=None):
        """Helper per creare handler con mock"""
        handler = UserRatesHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()
        handler.request.headers = headers or {}
        return handler

    def test_get_success(self):
        """Test GET tariffe utente"""
        handler = self.create_handler(
            headers={"X-Telegram-Init-Data": "valid"},
        )

        with (
            patch("api.handlers.validate_init_data") as mock_auth,
            patch("api.handlers.load_user") as mock_user,
        ):
            mock_auth.return_value = mock_valid_auth(user_id=123456789)
            mock_user.return_value = {
                "luce": {
                    "tipo": "variabile",
                    "fascia": "monoraria",
                    "energia": 0.090,
                    "commercializzazione": 72.0,
                },
                "gas": None,
            }

            handler.get()

            handler.set_status.assert_called_with(200)
            response = json.loads(handler.write.call_args[0][0])
            assert response["success"] is True
            assert response["data"]["luce"]["tipo"] == "variabile"

            # Verifica che load_user sia chiamato con l'ID corretto
            mock_user.assert_called_once_with("123456789")

    def test_get_user_not_found(self):
        """Test GET utente non registrato"""
        handler = self.create_handler(
            headers={"X-Telegram-Init-Data": "valid"},
        )

        with (
            patch("api.handlers.validate_init_data") as mock_auth,
            patch("api.handlers.load_user") as mock_user,
        ):
            mock_auth.return_value = mock_valid_auth(user_id=999999)
            mock_user.return_value = None

            handler.get()

            handler.set_status.assert_called_with(404)
            response = json.loads(handler.write.call_args[0][0])
            assert response["success"] is False
            assert "not found" in response["error"].lower()

    def test_get_extracts_user_id_from_auth(self):
        """Test che estrae correttamente user_id dall'auth"""
        handler = self.create_handler(
            headers={"X-Telegram-Init-Data": "valid"},
        )

        with (
            patch("api.handlers.validate_init_data") as mock_auth,
            patch("api.handlers.load_user") as mock_user,
        ):
            mock_auth.return_value = mock_valid_auth(user_id=987654321)
            mock_user.return_value = {"luce": {"tipo": "fissa"}}

            handler.get()

            # Verifica che load_user sia chiamato con l'ID estratto
            mock_user.assert_called_once_with("987654321")


class TestAPIErrorHandling:
    """Test gestione errori comune a tutti gli handler"""

    def test_options_preflight(self):
        """Test preflight CORS request ritorna 204"""
        handler = RatesHistoryHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()

        handler.options()

        handler.set_status.assert_called_with(204)
        handler.finish.assert_called_once()

    def test_missing_tipo(self):
        """Test GET senza parametro tipo"""
        handler = RatesHistoryHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()
        handler.get_argument = Mock(
            side_effect=lambda k, default=None: {"servizio": "luce"}.get(k, default)
        )
        handler.request.headers = {"X-Telegram-Init-Data": "valid"}

        with patch("api.handlers.validate_init_data") as mock_auth:
            mock_auth.return_value = mock_valid_auth()
            handler.get()

        handler.set_status.assert_called_with(400)
        response = json.loads(handler.write.call_args[0][0])
        assert "tipo" in response["error"].lower()

    def test_invalid_tipo(self):
        """Test GET con tipo non valido"""
        handler = RatesHistoryHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()
        handler.get_argument = Mock(
            side_effect=lambda k, default=None: {"servizio": "luce", "tipo": "invalido"}.get(
                k, default
            )
        )
        handler.request.headers = {"X-Telegram-Init-Data": "valid"}

        with patch("api.handlers.validate_init_data") as mock_auth:
            mock_auth.return_value = mock_valid_auth()
            handler.get()

        handler.set_status.assert_called_with(400)
        response = json.loads(handler.write.call_args[0][0])
        assert "tipo" in response["error"].lower()

    def test_missing_fascia(self):
        """Test GET senza parametro fascia"""
        handler = RatesHistoryHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()
        handler.get_argument = Mock(
            side_effect=lambda k, default=None: {"servizio": "luce", "tipo": "fissa"}.get(
                k, default
            )
        )
        handler.request.headers = {"X-Telegram-Init-Data": "valid"}

        with patch("api.handlers.validate_init_data") as mock_auth:
            mock_auth.return_value = mock_valid_auth()
            handler.get()

        handler.set_status.assert_called_with(400)
        response = json.loads(handler.write.call_args[0][0])
        assert "fascia" in response["error"].lower()

    def test_invalid_fascia(self):
        """Test GET con fascia non valida"""
        handler = RatesHistoryHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()
        handler.get_argument = Mock(
            side_effect=lambda k, default=None: {
                "servizio": "luce",
                "tipo": "fissa",
                "fascia": "invalida",
            }.get(k, default)
        )
        handler.request.headers = {"X-Telegram-Init-Data": "valid"}

        with patch("api.handlers.validate_init_data") as mock_auth:
            mock_auth.return_value = mock_valid_auth()
            handler.get()

        handler.set_status.assert_called_with(400)
        response = json.loads(handler.write.call_args[0][0])
        assert "fascia" in response["error"].lower()

    def test_days_negative_defaults_to_365(self):
        """Test GET con days negativo usa default 365"""
        handler = RatesHistoryHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()
        handler.get_argument = Mock(
            side_effect=lambda k, default=None: {
                "servizio": "luce",
                "tipo": "fissa",
                "fascia": "monoraria",
                "days": "-5",
            }.get(k, default)
        )
        handler.request.headers = {"X-Telegram-Init-Data": "valid"}

        with (
            patch("api.handlers.validate_init_data") as mock_auth,
            patch("api.handlers.get_rate_history") as mock_history,
        ):
            mock_auth.return_value = mock_valid_auth()
            mock_history.return_value = {"labels": [], "data": [], "period": {}}
            handler.get()

        mock_history.assert_called_once()
        assert mock_history.call_args[1].get("days") == 365

    def test_days_exceeds_max_capped_to_3650(self):
        """Test GET con days > 3650 viene limitato a 3650"""
        handler = RatesHistoryHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()
        handler.get_argument = Mock(
            side_effect=lambda k, default=None: {
                "servizio": "luce",
                "tipo": "fissa",
                "fascia": "monoraria",
                "days": "9999",
            }.get(k, default)
        )
        handler.request.headers = {"X-Telegram-Init-Data": "valid"}

        with (
            patch("api.handlers.validate_init_data") as mock_auth,
            patch("api.handlers.get_rate_history") as mock_history,
        ):
            mock_auth.return_value = mock_valid_auth()
            mock_history.return_value = {"labels": [], "data": [], "period": {}}
            handler.get()

        mock_history.assert_called_once()
        assert mock_history.call_args[1].get("days") == 3650

    def test_days_invalid_string_defaults_to_365(self):
        """Test GET con days non numerico usa default 365"""
        handler = RatesHistoryHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()
        handler.get_argument = Mock(
            side_effect=lambda k, default=None: {
                "servizio": "luce",
                "tipo": "fissa",
                "fascia": "monoraria",
                "days": "abc",
            }.get(k, default)
        )
        handler.request.headers = {"X-Telegram-Init-Data": "valid"}

        with (
            patch("api.handlers.validate_init_data") as mock_auth,
            patch("api.handlers.get_rate_history") as mock_history,
        ):
            mock_auth.return_value = mock_valid_auth()
            mock_history.return_value = {"labels": [], "data": [], "period": {}}
            handler.get()

        mock_history.assert_called_once()
        assert mock_history.call_args[1].get("days") == 365

    def test_rates_current_auth_error(self):
        """Test RatesCurrentHandler con autenticazione non valida"""
        handler = RatesCurrentHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()
        handler.request.headers = {"X-Telegram-Init-Data": "invalid"}

        with patch("api.handlers.validate_init_data") as mock_auth:
            from api.auth import TelegramAuthError

            mock_auth.side_effect = TelegramAuthError("Invalid signature")
            handler.get()

        handler.set_status.assert_called_with(401)

    def test_rates_current_internal_error(self):
        """Test RatesCurrentHandler con errore interno"""
        handler = RatesCurrentHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()
        handler.request.headers = {"X-Telegram-Init-Data": "valid"}

        with (
            patch("api.handlers.validate_init_data") as mock_auth,
            patch("api.handlers.get_current_rates") as mock_rates,
        ):
            mock_auth.return_value = mock_valid_auth()
            mock_rates.side_effect = Exception("DB crash")
            handler.get()

        handler.set_status.assert_called_with(500)
        response = json.loads(handler.write.call_args[0][0])
        assert response["success"] is False

    def test_rates_current_missing_auth(self):
        """Test RatesCurrentHandler senza header auth"""
        handler = RatesCurrentHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()
        handler.request.headers = {}

        handler.get()

        handler.set_status.assert_called_with(401)

    def test_user_rates_auth_error(self):
        """Test UserRatesHandler con autenticazione non valida"""
        handler = UserRatesHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()
        handler.request.headers = {"X-Telegram-Init-Data": "invalid"}

        with patch("api.handlers.validate_init_data") as mock_auth:
            from api.auth import TelegramAuthError

            mock_auth.side_effect = TelegramAuthError("Invalid signature")
            handler.get()

        handler.set_status.assert_called_with(401)

    def test_user_rates_internal_error(self):
        """Test UserRatesHandler con errore interno"""
        handler = UserRatesHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()
        handler.request.headers = {"X-Telegram-Init-Data": "valid"}

        with (
            patch("api.handlers.validate_init_data") as mock_auth,
            patch("api.handlers.load_user") as mock_user,
        ):
            mock_auth.return_value = mock_valid_auth()
            mock_user.side_effect = Exception("DB crash")
            handler.get()

        handler.set_status.assert_called_with(500)
        response = json.loads(handler.write.call_args[0][0])
        assert response["success"] is False

    def test_internal_server_error(self):
        """Test gestione exception interna"""
        handler = RatesHistoryHandler(
            application=MagicMock(),
            request=MagicMock(),
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()
        handler.finish = Mock()

        # Mock get_argument per ritornare valori validi per tutti i parametri
        def mock_get_argument(key, default=None):
            args = {
                "servizio": "luce",
                "tipo": "fissa",
                "fascia": "monoraria",
                "days": "30",
            }
            return args.get(key, default)

        handler.get_argument = Mock(side_effect=mock_get_argument)
        handler.request.headers = {"X-Telegram-Init-Data": "valid"}

        with (
            patch("api.handlers.validate_init_data") as mock_auth,
            patch("api.handlers.get_rate_history") as mock_history,
        ):
            mock_auth.return_value = mock_valid_auth()
            mock_history.side_effect = Exception("Unexpected error")

            handler.get()

            # Deve ritornare 500
            handler.set_status.assert_called_with(500)
            response = json.loads(handler.write.call_args[0][0])
            assert response["success"] is False
            assert "error" in response
