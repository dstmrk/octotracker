#!/usr/bin/env python3
"""
Bot Telegram per registrare le tariffe degli utenti
"""
import os
import json
from pathlib import Path
from telegram import Update
from telegram.ext import Application, CommandHandler, MessageHandler, filters, ContextTypes, ConversationHandler
from dotenv import load_dotenv

load_dotenv()

# Stati conversazione
LUCE_ENERGIA, LUCE_COMM, GAS_ENERGIA, GAS_COMM = range(4)

# File dati
DATA_DIR = Path(__file__).parent / "data"
USERS_FILE = DATA_DIR / "users.json"

def load_users():
    """Carica dati utenti"""
    if USERS_FILE.exists():
        with open(USERS_FILE, 'r') as f:
            return json.load(f)
    return {}

def save_users(users):
    """Salva dati utenti"""
    DATA_DIR.mkdir(exist_ok=True)
    with open(USERS_FILE, 'w') as f:
        json.dump(users, f, indent=2)

async def start(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Avvia registrazione tariffe"""
    await update.message.reply_text(
        "👋 Ciao! Ti aiuto a monitorare le tariffe Octopus Energy.\n\n"
        "Inserisci le tue tariffe attuali.\n\n"
        "💡 **LUCE - Costo Energia** (€/kWh)\n"
        "Esempio: 0.12"
    )
    return LUCE_ENERGIA

async def luce_energia(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Salva costo energia luce"""
    try:
        context.user_data['luce_energia'] = float(update.message.text.replace(',', '.'))
        await update.message.reply_text(
            "💡 **LUCE - Costo Commercializzazione** (€/mese)\n"
            "Esempio: 8.50"
        )
        return LUCE_COMM
    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 0.12)")
        return LUCE_ENERGIA

async def luce_comm(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Salva costo commercializzazione luce"""
    try:
        context.user_data['luce_comm'] = float(update.message.text.replace(',', '.'))
        await update.message.reply_text(
            "🔥 **GAS - Costo Energia** (€/Smc)\n"
            "Esempio: 0.45"
        )
        return GAS_ENERGIA
    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 8.50)")
        return LUCE_COMM

async def gas_energia(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Salva costo energia gas"""
    try:
        context.user_data['gas_energia'] = float(update.message.text.replace(',', '.'))
        await update.message.reply_text(
            "🔥 **GAS - Costo Commercializzazione** (€/mese)\n"
            "Esempio: 12.00"
        )
        return GAS_COMM
    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 0.45)")
        return GAS_ENERGIA

async def gas_comm(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Salva tutto e conferma"""
    try:
        context.user_data['gas_comm'] = float(update.message.text.replace(',', '.'))

        # Salva nel file
        users = load_users()
        user_id = str(update.effective_user.id)
        users[user_id] = {
            'luce_energia': context.user_data['luce_energia'],
            'luce_comm': context.user_data['luce_comm'],
            'gas_energia': context.user_data['gas_energia'],
            'gas_comm': context.user_data['gas_comm']
        }
        save_users(users)

        await update.message.reply_text(
            "✅ **Tariffe salvate!**\n\n"
            f"💡 Luce:\n"
            f"  - Energia: €{context.user_data['luce_energia']:.3f}/kWh\n"
            f"  - Commercializzazione: €{context.user_data['luce_comm']:.2f}/mese\n\n"
            f"🔥 Gas:\n"
            f"  - Energia: €{context.user_data['gas_energia']:.3f}/Smc\n"
            f"  - Commercializzazione: €{context.user_data['gas_comm']:.2f}/mese\n\n"
            "Riceverai notifiche quando troverò tariffe più convenienti!\n\n"
            "Usa /mytariffe per vedere le tue tariffe salvate."
        )
        return ConversationHandler.END

    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 12.00)")
        return GAS_COMM

async def cancel(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Annulla registrazione"""
    await update.message.reply_text("❌ Registrazione annullata. Usa /start per ricominciare.")
    return ConversationHandler.END

async def my_tariffe(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Mostra tariffe salvate"""
    users = load_users()
    user_id = str(update.effective_user.id)

    if user_id not in users:
        await update.message.reply_text(
            "❌ Non hai ancora salvato le tue tariffe.\n"
            "Usa /start per registrarle."
        )
        return

    data = users[user_id]
    await update.message.reply_text(
        "📊 **Le tue tariffe attuali:**\n\n"
        f"💡 Luce:\n"
        f"  - Energia: €{data['luce_energia']:.3f}/kWh\n"
        f"  - Commercializzazione: €{data['luce_comm']:.2f}/mese\n\n"
        f"🔥 Gas:\n"
        f"  - Energia: €{data['gas_energia']:.3f}/Smc\n"
        f"  - Commercializzazione: €{data['gas_comm']:.2f}/mese\n\n"
        "Per modificarle usa /start"
    )

def main():
    """Avvia il bot"""
    token = os.getenv('TELEGRAM_BOT_TOKEN')
    if not token:
        raise ValueError("TELEGRAM_BOT_TOKEN non impostato in .env")

    app = Application.builder().token(token).build()

    # Handler conversazione registrazione
    conv_handler = ConversationHandler(
        entry_points=[CommandHandler('start', start)],
        states={
            LUCE_ENERGIA: [MessageHandler(filters.TEXT & ~filters.COMMAND, luce_energia)],
            LUCE_COMM: [MessageHandler(filters.TEXT & ~filters.COMMAND, luce_comm)],
            GAS_ENERGIA: [MessageHandler(filters.TEXT & ~filters.COMMAND, gas_energia)],
            GAS_COMM: [MessageHandler(filters.TEXT & ~filters.COMMAND, gas_comm)],
        },
        fallbacks=[CommandHandler('cancel', cancel)]
    )

    app.add_handler(conv_handler)
    app.add_handler(CommandHandler('mytariffe', my_tariffe))

    print("🤖 Bot avviato!")
    app.run_polling(allowed_updates=Update.ALL_TYPES)

if __name__ == '__main__':
    main()
