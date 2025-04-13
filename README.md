# Telegram Price Bot

A simple Telegram bot written in Go that fetches the current price of Bitcoin and Gold (per ounce) in USD, as well as USD to IRR (Iranian Rial) exchange rate and Gold price in IRR.

## Features

- Get Bitcoin price in USD
- Get Gold price in USD
- Get USD to IRR exchange rate
- Get Gold price in IRR (Iranian Rials)
- Get all prices at once
- Hourly automatic price updates (price cache refreshes every hour)
- Subscribe to automatic price updates at custom intervals
- Use in both private chats and group chats/channels

## Commands

- `/start` - Starts the bot
- `/help` - Shows available commands
- `/price` - Get all available prices (Bitcoin, Gold in USD, USD to IRR, Gold in IRR)
- `/bitcoin` - Get Bitcoin price in USD
- `/gold` - Get Gold price in USD
- `/usd` - Get USD to IRR exchange rate
- `/goldirr` - Get Gold price in IRR
- `/refresh` - Force refresh of all price data
- `/subscribe <minutes>` - Subscribe to price updates (e.g., `/subscribe 30` for updates every 30 minutes)
- `/unsubscribe` - Stop receiving price updates
- `/status` - Check subscription status and cache information

## How Price Caching Works

The bot automatically fetches and caches all price data:

1. **Hourly Updates**: The price data is automatically refreshed at the top of each hour
2. **Command Responses**: All price commands first check the cache before fetching fresh data
3. **Cache Validity**: Cached data is considered valid for up to 70 minutes
4. **Manual Refresh**: You can force a cache refresh with the `/refresh` command

## Group/Channel Usage

1. Add the bot to your group or channel
2. Make the bot an admin (for channels) to allow it to post messages
3. Use the `/subscribe` command to set up automatic updates
4. The bot will automatically send price updates at the specified interval

## Setup

### Running Locally

1. Create a Telegram bot via [BotFather](https://t.me/botfather) and get your bot token
2. Set the required environment variables:

```bash
# For Windows PowerShell
$env:TELEGRAM_BOT_TOKEN="your_telegram_bot_token"

# For Linux/macOS
export TELEGRAM_BOT_TOKEN="your_telegram_bot_token"
```

3. Run the bot:

```bash
go run main.go
```

### Docker Deployment

1. Create a Telegram bot via [BotFather](https://t.me/botfather) and get your bot token
2. Create a `.env` file with your bot token (you can copy from `.env.example`):

```bash
cp .env.example .env
```

3. Edit the `.env` file to add your bot token:

```
TELEGRAM_BOT_TOKEN=your_telegram_bot_token_here
```

4. Build and run with Docker Compose:

```bash
docker-compose up -d
```

5. Check logs:

```bash
docker-compose logs -f
```

6. To stop the bot:

```bash
docker-compose down
```

## API Sources

- Bitcoin price: [CoinGecko API](https://www.coingecko.com/en/api)
- Gold price in USD: [Metals.live API](https://metals.live/)
- USD to IRR exchange rate: [TGJU.org](https://www.tgju.org/%D9%82%DB%8C%D9%85%D8%AA-%D8%AF%D9%84%D8%A7%D8%B1)
- Gold price in IRR: [TGJU.org](https://www.tgju.org/gold-chart)

## Note

For production use, you might want to:
- Add error handling for API rate limits
- Implement database storage for the price cache
- Add monitoring for failed scraping attempts
- Set up a proxy for more reliable access to the websites being scraped
- Persist subscriptions to a database to survive bot restarts

## Project Structure

```
├── main.go              # Main application code
├── .env.example         # Example environment variables
├── .env                 # Environment variables (not committed to git)
├── Dockerfile           # Docker configuration
├── docker-compose.yml   # Docker Compose configuration
├── .gitignore           # Git ignore file
└── logs/                # Generated logs directory
``` 