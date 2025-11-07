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

def check_better_rates(user_rates, current_rates):
    """
    Confronta tariffe utente con tariffe attuali
    Ritorna dizionario con risparmi trovati
    """
    savings = {
        'luce_energia': None,
        'luce_comm': None,
        'gas_energia': None,
        'gas_comm': None,
        'has_savings': False
    }

    # Controlla luce
    if current_rates.get('luce'):
        if current_rates['luce'].get('energia') and current_rates['luce']['energia'] < user_rates['luce_energia']:
            savings['luce_energia'] = {
                'attuale': user_rates['luce_energia'],
                'nuova': current_rates['luce']['energia'],
                'risparmio': user_rates['luce_energia'] - current_rates['luce']['energia']
            }
            savings['has_savings'] = True

        if current_rates['luce'].get('commercializzazione') and current_rates['luce']['commercializzazione'] < user_rates['luce_comm']:
            savings['luce_comm'] = {
                'attuale': user_rates['luce_comm'],
                'nuova': current_rates['luce']['commercializzazione'],
                'risparmio': user_rates['luce_comm'] - current_rates['luce']['commercializzazione']
            }
            savings['has_savings'] = True

    # Controlla gas (solo se l'utente ha il gas)
    if current_rates.get('gas') and user_rates.get('gas_energia') is not None:
        if current_rates['gas'].get('energia') and current_rates['gas']['energia'] < user_rates['gas_energia']:
            savings['gas_energia'] = {
                'attuale': user_rates['gas_energia'],
                'nuova': current_rates['gas']['energia'],
                'risparmio': user_rates['gas_energia'] - current_rates['gas']['energia']
            }
            savings['has_savings'] = True

        if current_rates['gas'].get('commercializzazione') and user_rates.get('gas_comm') is not None and current_rates['gas']['commercializzazione'] < user_rates['gas_comm']:
            savings['gas_comm'] = {
                'attuale': user_rates['gas_comm'],
                'nuova': current_rates['gas']['commercializzazione'],
                'risparmio': user_rates['gas_comm'] - current_rates['gas']['commercializzazione']
            }
            savings['has_savings'] = True

    return savings

def format_notification(savings):
    """Formatta messaggio di notifica"""
    message = "🎉 **Trovate tariffe più convenienti!**\n\n"

    if savings['luce_energia']:
        s = savings['luce_energia']
        message += f"💡 **LUCE - Energia**\n"
        message += f"  Attuale: €{s['attuale']:.3f}/kWh\n"
        message += f"  Nuova: €{s['nuova']:.3f}/kWh\n"
        message += f"  ✅ Risparmi: €{s['risparmio']:.3f}/kWh\n\n"

    if savings['luce_comm']:
        s = savings['luce_comm']
        message += f"💡 **LUCE - Commercializzazione**\n"
        message += f"  Attuale: €{s['attuale']:.2f}/anno\n"
        message += f"  Nuova: €{s['nuova']:.2f}/anno\n"
        message += f"  ✅ Risparmi: €{s['risparmio']:.2f}/anno\n\n"

    if savings['gas_energia']:
        s = savings['gas_energia']
        message += f"🔥 **GAS - Energia**\n"
        message += f"  Attuale: €{s['attuale']:.3f}/Smc\n"
        message += f"  Nuova: €{s['nuova']:.3f}/Smc\n"
        message += f"  ✅ Risparmi: €{s['risparmio']:.3f}/Smc\n\n"

    if savings['gas_comm']:
        s = savings['gas_comm']
        message += f"🔥 **GAS - Commercializzazione**\n"
        message += f"  Attuale: €{s['attuale']:.2f}/anno\n"
        message += f"  Nuova: €{s['nuova']:.2f}/anno\n"
        message += f"  ✅ Risparmi: €{s['risparmio']:.2f}/anno\n\n"

    message += "🔗 Controlla le tariffe su: https://octopusenergy.it/le-nostre-tariffe"

    return message

async def send_notification(bot, user_id, message):
    """Invia notifica Telegram"""
    try:
        await bot.send_message(chat_id=user_id, text=message, parse_mode='Markdown')
        return True
    except Exception as e:
        print(f"❌ Errore invio messaggio a {user_id}: {e}")
        return False

async def main():
    """Controlla tariffe e invia notifiche"""
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
    token = os.getenv('TELEGRAM_BOT_TOKEN')
    if not token:
        raise ValueError("TELEGRAM_BOT_TOKEN non impostato in .env")

    bot = Bot(token=token)

    # Controlla ogni utente
    notifications_sent = 0
    for user_id, user_rates in users.items():
        print(f"📊 Controllo utente {user_id}...")

        savings = check_better_rates(user_rates, current_rates)

        if savings['has_savings']:
            message = format_notification(savings)
            success = await send_notification(bot, user_id, message)
            if success:
                notifications_sent += 1
                print(f"  ✅ Notifica inviata")
        else:
            print(f"  ℹ️  Nessun risparmio trovato")

    print(f"\n✅ Controllo completato. Notifiche inviate: {notifications_sent}/{len(users)}")

if __name__ == '__main__':
    asyncio.run(main())
