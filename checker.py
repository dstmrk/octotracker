#!/usr/bin/env python3
"""
Controlla se ci sono tariffe più convenienti e notifica gli utenti
"""
import os
import json
from pathlib import Path
from telegram import Bot
from dotenv import load_dotenv
import asyncio

load_dotenv()

# File dati
DATA_DIR = Path(__file__).parent / "data"
USERS_FILE = DATA_DIR / "users.json"
RATES_FILE = DATA_DIR / "current_rates.json"

def load_json(file_path):
    """Carica file JSON"""
    if file_path.exists():
        with open(file_path, 'r') as f:
            return json.load(f)
    return None

def save_users(users):
    """Salva dati utenti"""
    DATA_DIR.mkdir(exist_ok=True)
    with open(USERS_FILE, 'w') as f:
        json.dump(users, f, indent=2)

def check_better_rates(user_rates, current_rates):
    """
    Confronta tariffe utente con tariffe attuali
    Ritorna dizionario con risparmi e peggioramenti trovati
    """
    savings = {
        'luce_energia': None,
        'luce_comm': None,
        'gas_energia': None,
        'gas_comm': None,
        'luce_energia_worse': False,
        'luce_comm_worse': False,
        'gas_energia_worse': False,
        'gas_comm_worse': False,
        'has_savings': False,
        'is_mixed': False
    }

    # Controlla luce
    if current_rates.get('luce'):
        if current_rates['luce'].get('energia'):
            if current_rates['luce']['energia'] < user_rates['luce_energia']:
                savings['luce_energia'] = {
                    'attuale': user_rates['luce_energia'],
                    'nuova': current_rates['luce']['energia'],
                    'risparmio': user_rates['luce_energia'] - current_rates['luce']['energia']
                }
                savings['has_savings'] = True
            elif current_rates['luce']['energia'] > user_rates['luce_energia']:
                savings['luce_energia_worse'] = True

        if current_rates['luce'].get('commercializzazione'):
            if current_rates['luce']['commercializzazione'] < user_rates['luce_comm']:
                savings['luce_comm'] = {
                    'attuale': user_rates['luce_comm'],
                    'nuova': current_rates['luce']['commercializzazione'],
                    'risparmio': user_rates['luce_comm'] - current_rates['luce']['commercializzazione']
                }
                savings['has_savings'] = True
            elif current_rates['luce']['commercializzazione'] > user_rates['luce_comm']:
                savings['luce_comm_worse'] = True

    # Controlla gas (solo se l'utente ha il gas)
    if current_rates.get('gas') and user_rates.get('gas_energia') is not None:
        if current_rates['gas'].get('energia'):
            if current_rates['gas']['energia'] < user_rates['gas_energia']:
                savings['gas_energia'] = {
                    'attuale': user_rates['gas_energia'],
                    'nuova': current_rates['gas']['energia'],
                    'risparmio': user_rates['gas_energia'] - current_rates['gas']['energia']
                }
                savings['has_savings'] = True
            elif current_rates['gas']['energia'] > user_rates['gas_energia']:
                savings['gas_energia_worse'] = True

        if current_rates['gas'].get('commercializzazione') and user_rates.get('gas_comm') is not None:
            if current_rates['gas']['commercializzazione'] < user_rates['gas_comm']:
                savings['gas_comm'] = {
                    'attuale': user_rates['gas_comm'],
                    'nuova': current_rates['gas']['commercializzazione'],
                    'risparmio': user_rates['gas_comm'] - current_rates['gas']['commercializzazione']
                }
                savings['has_savings'] = True
            elif current_rates['gas']['commercializzazione'] > user_rates['gas_comm']:
                savings['gas_comm_worse'] = True

    # Determina se è un caso "mixed" (una componente migliora, l'altra peggiora)
    # Per luce
    luce_has_improvement = savings['luce_energia'] or savings['luce_comm']
    luce_has_worsening = savings['luce_energia_worse'] or savings['luce_comm_worse']

    # Per gas
    gas_has_improvement = savings['gas_energia'] or savings['gas_comm']
    gas_has_worsening = savings['gas_energia_worse'] or savings['gas_comm_worse']

    # È mixed se almeno una componente (luce o gas) ha sia miglioramenti che peggioramenti
    if (luce_has_improvement and luce_has_worsening) or (gas_has_improvement and gas_has_worsening):
        savings['is_mixed'] = True

    return savings

def format_notification(savings, user_rates, current_rates):
    """Formatta messaggio di notifica"""
    # Header diverso per caso mixed vs tutto migliorato
    if savings['is_mixed']:
        message = "⚖️ <b>Aggiornamento tariffe Octopus Energy</b>\n"
        message += "OctoTracker ha rilevato una variazione nelle tariffe, ma non è detto che sia automaticamente più conveniente: una delle due componenti è migliorata, l'altra è aumentata.\n\n"
    else:
        message = "⚡️ <b>Buone notizie!</b>\n"
        message += "OctoTracker ha trovato una tariffa Octopus Energy più conveniente rispetto a quella che hai attiva.\n\n"

    # Mostra Luce se c'è risparmio o peggioramento in energia O commercializzazione
    if savings['luce_energia'] or savings['luce_comm'] or savings['luce_energia_worse'] or savings['luce_comm_worse']:
        message += "💡 <b>Luce:</b>\n"
        message += f"Tua tariffa: {user_rates['luce_energia']:.3f} €/kWh, {user_rates['luce_comm']:.0f} €/anno\n"

        # Formatta valori: grassetto per miglioramenti, sottolineato per peggioramenti
        energia_new = current_rates['luce']['energia']
        comm_new = current_rates['luce']['commercializzazione']

        if savings['luce_energia']:
            energia_str = f"<b>{energia_new:.3f} €/kWh</b>"
        elif savings['luce_energia_worse']:
            energia_str = f"<u>{energia_new:.3f} €/kWh</u>"
        else:
            energia_str = f"{energia_new:.3f} €/kWh"

        if savings['luce_comm']:
            comm_str = f"<b>{comm_new:.0f} €/anno</b>"
        elif savings['luce_comm_worse']:
            comm_str = f"<u>{comm_new:.0f} €/anno</u>"
        else:
            comm_str = f"{comm_new:.0f} €/anno"

        message += f"Nuova tariffa: {energia_str}, {comm_str}\n\n"

    # Mostra Gas se c'è risparmio o peggioramento in energia O commercializzazione (e se l'utente ha il gas)
    if user_rates.get('gas_energia') is not None and (savings['gas_energia'] or savings['gas_comm'] or savings['gas_energia_worse'] or savings['gas_comm_worse']):
        message += "🔥 <b>Gas:</b>\n"
        message += f"Tua tariffa: {user_rates['gas_energia']:.2f} €/Smc, {user_rates['gas_comm']:.0f} €/anno\n"

        # Formatta valori: grassetto per miglioramenti, sottolineato per peggioramenti
        energia_new = current_rates['gas']['energia']
        comm_new = current_rates['gas']['commercializzazione']

        if savings['gas_energia']:
            energia_str = f"<b>{energia_new:.2f} €/Smc</b>"
        elif savings['gas_energia_worse']:
            energia_str = f"<u>{energia_new:.2f} €/Smc</u>"
        else:
            energia_str = f"{energia_new:.2f} €/Smc"

        if savings['gas_comm']:
            comm_str = f"<b>{comm_new:.0f} €/anno</b>"
        elif savings['gas_comm_worse']:
            comm_str = f"<u>{comm_new:.0f} €/anno</u>"
        else:
            comm_str = f"{comm_new:.0f} €/anno"

        message += f"Nuova tariffa: {energia_str}, {comm_str}\n\n"

    # Footer diverso per caso mixed
    if savings['is_mixed']:
        message += "📊 In questi casi la convenienza dipende dai tuoi consumi.\n"
        message += "Ti consiglio di fare una verifica in base ai kWh/Smc che usi mediamente ogni anno, puoi trovare i dati nelle tue bollette.\n\n"
    else:
        message += "💬 Il confronto tiene conto sia del prezzo dell'energia che del costo di commercializzazione.\n\n"

    message += "🔗 Maggiori info: https://octopusenergy.it/le-nostre-tariffe"

    return message

async def send_notification(bot, user_id, message):
    """Invia notifica Telegram"""
    try:
        await bot.send_message(chat_id=user_id, text=message, parse_mode='HTML')
        return True
    except Exception as e:
        print(f"❌ Errore invio messaggio a {user_id}: {e}")
        return False

async def check_and_notify_users(bot_token: str):
    """Controlla tariffe e invia notifiche (chiamata da bot.py)"""
    print("🔍 Inizio controllo tariffe...")

    # Carica dati
    users = load_json(USERS_FILE)
    current_rates = load_json(RATES_FILE)

    if not users:
        print("⚠️  Nessun utente registrato")
        return

    if not current_rates:
        print("❌ Nessuna tariffa disponibile. Esegui prima scraper.py")
        return

    # Inizializza bot
    bot = Bot(token=bot_token)

    # Controlla ogni utente
    notifications_sent = 0
    users_updated = False

    for user_id, user_rates in users.items():
        print(f"📊 Controllo utente {user_id}...")

        savings = check_better_rates(user_rates, current_rates)

        if savings['has_savings']:
            # Costruisci oggetto con tariffe Octopus attuali
            current_octopus = {
                'luce_energia': current_rates['luce']['energia'],
                'luce_comm': current_rates['luce']['commercializzazione']
            }

            # Aggiungi gas solo se l'utente ce l'ha e se sono disponibili
            if current_rates.get('gas') and user_rates.get('gas_energia') is not None:
                current_octopus['gas_energia'] = current_rates['gas']['energia']
                current_octopus['gas_comm'] = current_rates['gas']['commercializzazione']

            # Controlla se abbiamo già notificato queste stesse tariffe
            last_notified = user_rates.get('last_notified_rates', {})

            if last_notified == current_octopus:
                print(f"  ⏭️  Tariffe migliori già notificate in precedenza, skip")
            else:
                # Tariffe diverse o prima notifica - invia messaggio
                message = format_notification(savings, user_rates, current_rates)
                success = await send_notification(bot, user_id, message)
                if success:
                    # Aggiorna last_notified_rates per questo utente
                    users[user_id]['last_notified_rates'] = current_octopus
                    users_updated = True
                    notifications_sent += 1
                    print(f"  ✅ Notifica inviata e tariffe salvate")
        else:
            print(f"  ℹ️  Nessun risparmio trovato")

    # Salva users.json se ci sono stati aggiornamenti
    if users_updated:
        save_users(users)
        print(f"💾 Dati utenti aggiornati")

    print(f"\n✅ Controllo completato. Notifiche inviate: {notifications_sent}/{len(users)}")

async def main():
    """Main per esecuzione standalone"""
    token = os.getenv('TELEGRAM_BOT_TOKEN')
    if not token:
        raise ValueError("TELEGRAM_BOT_TOKEN non impostato in .env")
    await check_and_notify_users(token)

if __name__ == '__main__':
    asyncio.run(main())
