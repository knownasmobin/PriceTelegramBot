package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// PriceCache stores cached price data
type PriceCache struct {
	BitcoinUSD float64
	GoldUSD    float64
	UsdToIrr   string
	GoldIrr    string
	LastUpdate time.Time
	mutex      sync.RWMutex
}

// Global cache instance
var priceCache = PriceCache{
	LastUpdate: time.Time{}, // Zero time (never updated)
}

func (pc *PriceCache) updateBitcoinPrice() error {
	price, err := fetchBitcoinPrice()
	if err != nil {
		return err
	}

	pc.mutex.Lock()
	pc.BitcoinUSD = price
	pc.mutex.Unlock()
	return nil
}

func (pc *PriceCache) updateGoldPrice() error {
	price, err := fetchGoldPrice()
	if err != nil {
		return err
	}

	pc.mutex.Lock()
	pc.GoldUSD = price
	pc.mutex.Unlock()
	return nil
}

func (pc *PriceCache) updateUsdToIrrPrice() error {
	price, err := fetchUsdToIrrPrice()
	if err != nil {
		return err
	}

	pc.mutex.Lock()
	pc.UsdToIrr = price
	pc.mutex.Unlock()
	return nil
}

func (pc *PriceCache) updateGoldIrrPrice() error {
	price, err := fetchGoldPriceInIRR()
	if err != nil {
		return err
	}

	pc.mutex.Lock()
	pc.GoldIrr = price
	pc.mutex.Unlock()
	return nil
}

// refreshCache updates all cached prices
func (pc *PriceCache) refreshCache() {
	log.Println("Refreshing price cache...")

	var wg sync.WaitGroup
	wg.Add(4)

	// Update Bitcoin price
	go func() {
		defer wg.Done()
		if err := pc.updateBitcoinPrice(); err != nil {
			log.Printf("Error updating Bitcoin price: %v", err)
		} else {
			log.Println("Bitcoin price updated successfully")
		}
	}()

	// Update Gold price
	go func() {
		defer wg.Done()
		if err := pc.updateGoldPrice(); err != nil {
			log.Printf("Error updating Gold price: %v", err)
		} else {
			log.Println("Gold price updated successfully")
		}
	}()

	// Update USD to IRR exchange rate
	go func() {
		defer wg.Done()
		if err := pc.updateUsdToIrrPrice(); err != nil {
			log.Printf("Error updating USD to IRR exchange rate: %v", err)
		} else {
			log.Println("USD to IRR exchange rate updated successfully")
		}
	}()

	// Update Gold IRR price
	go func() {
		defer wg.Done()
		if err := pc.updateGoldIrrPrice(); err != nil {
			log.Printf("Error updating Gold IRR price: %v", err)
		} else {
			log.Println("Gold IRR price updated successfully")
		}
	}()

	// Wait for all updates to complete
	wg.Wait()

	// Update last update time
	pc.mutex.Lock()
	pc.LastUpdate = time.Now()
	pc.mutex.Unlock()

	log.Println("Price cache refresh completed")
}

// StartCacheRefresher starts a goroutine to refresh the cache periodically
func StartCacheRefresher() {
	// First immediate refresh
	priceCache.refreshCache()

	// Start periodic refresher at the top of each hour
	go func() {
		for {
			// Calculate time until the next hour
			now := time.Now()
			nextHour := now.Truncate(time.Hour).Add(time.Hour)
			duration := nextHour.Sub(now)

			// Wait until the next hour
			log.Printf("Next cache refresh scheduled in %v (at %s)", duration, nextHour.Format("15:04:05"))
			time.Sleep(duration)

			// Refresh the cache
			priceCache.refreshCache()
		}
	}()
}

type CoinGeckoResponse struct {
	Bitcoin struct {
		USD float64 `json:"usd"`
	} `json:"bitcoin"`
}

type MetalsAPIResponse struct {
	Rates struct {
		XAU float64 `json:"XAU"`
	} `json:"rates"`
}

// Subscription stores information about chat subscriptions
type Subscription struct {
	ChatID    int64
	ChatTitle string
	Interval  time.Duration // in minutes
}

// SubscriptionManager manages all active subscriptions
type SubscriptionManager struct {
	subscriptions map[int64]*Subscription
	mutex         sync.RWMutex
}

// NewSubscriptionManager creates a new subscription manager
func NewSubscriptionManager() *SubscriptionManager {
	return &SubscriptionManager{
		subscriptions: make(map[int64]*Subscription),
	}
}

// Subscribe adds a new subscription
func (sm *SubscriptionManager) Subscribe(chatID int64, chatTitle string, interval time.Duration) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	sm.subscriptions[chatID] = &Subscription{
		ChatID:    chatID,
		ChatTitle: chatTitle,
		Interval:  interval,
	}
}

// Unsubscribe removes a subscription
func (sm *SubscriptionManager) Unsubscribe(chatID int64) bool {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	if _, exists := sm.subscriptions[chatID]; exists {
		delete(sm.subscriptions, chatID)
		return true
	}
	return false
}

// GetSubscription returns a subscription by chat ID
func (sm *SubscriptionManager) GetSubscription(chatID int64) (*Subscription, bool) {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	sub, exists := sm.subscriptions[chatID]
	return sub, exists
}

// GetAllSubscriptions returns all active subscriptions
func (sm *SubscriptionManager) GetAllSubscriptions() []*Subscription {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	subs := make([]*Subscription, 0, len(sm.subscriptions))
	for _, sub := range sm.subscriptions {
		subs = append(subs, sub)
	}
	return subs
}

// fetchBitcoinPrice fetches the current Bitcoin price from API
func fetchBitcoinPrice() (float64, error) {
	url := "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=usd"

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var response CoinGeckoResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return 0, err
	}

	return response.Bitcoin.USD, nil
}

// getBitcoinPrice returns Bitcoin price (from cache if available)
func getBitcoinPrice() (float64, error) {
	priceCache.mutex.RLock()
	cachedPrice := priceCache.BitcoinUSD
	lastUpdate := priceCache.LastUpdate
	priceCache.mutex.RUnlock()

	// If we have a valid cached price (non-zero and not too old), use it
	if cachedPrice > 0 && time.Since(lastUpdate) < 70*time.Minute {
		return cachedPrice, nil
	}

	// Otherwise fetch fresh data
	return fetchBitcoinPrice()
}

// fetchGoldPrice fetches the current Gold price from API
func fetchGoldPrice() (float64, error) {
	// Using Metals API which provides gold price per troy ounce in USD
	// This API endpoint doesn't require authentication for limited use
	url := "https://api.gold-api.com/price/XAU"

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	// The new response is a single object with price field
	var response struct {
		Name              string  `json:"name"`
		Price             float64 `json:"price"`
		Symbol            string  `json:"symbol"`
		UpdatedAt         string  `json:"updatedAt"`
		UpdatedAtReadable string  `json:"updatedAtReadable"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return 0, err
	}

	return response.Price, nil
}

// getGoldPrice returns Gold price (from cache if available)
func getGoldPrice() (float64, error) {
	priceCache.mutex.RLock()
	cachedPrice := priceCache.GoldUSD
	lastUpdate := priceCache.LastUpdate
	priceCache.mutex.RUnlock()

	// If we have a valid cached price (non-zero and not too old), use it
	if cachedPrice > 0 && time.Since(lastUpdate) < 70*time.Minute {
		return cachedPrice, nil
	}

	// Otherwise fetch fresh data
	return fetchGoldPrice()
}

// fetchUsdToIrrPrice scrapes the USD to IRR exchange rate from tgju.org
func fetchUsdToIrrPrice() (string, error) {
	url := "https://www.tgju.org/%D9%82%DB%8C%D9%85%D8%AA-%D8%AF%D9%84%D8%A7%D8%B1"

	client := &http.Client{Timeout: 15 * time.Second}

	// Set custom headers to mimic browser request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Read the HTML content
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	bodyString := string(bodyBytes)

	// Try multiple patterns to find the USD to IRR price
	// First pattern: Look for the tooltip with the price
	regex := regexp.MustCompile(`data-market-row="price_dollar_rl"[^>]*data-title="[^"]*'tooltip-row-txt'>([0-9,]+)`)
	matches := regex.FindStringSubmatch(bodyString)

	if len(matches) < 2 {
		// Second pattern: Look directly for the table cell with the price
		regex = regexp.MustCompile(`<th><span class="flag flag-usd"><span></span></span> دلار</th>\s*<td class="nf">([0-9,]+)</td>`)
		matches = regex.FindStringSubmatch(bodyString)

		if len(matches) < 2 {
			// Third pattern: More general pattern
			regex = regexp.MustCompile(`"price_dollar_rl"[^>]*>.*?<td class="nf">([0-9,]+)</td>`)
			matches = regex.FindStringSubmatch(bodyString)

			if len(matches) < 2 {
				return "", fmt.Errorf("couldn't find USD to IRR price in the webpage")
			}
		}
	}

	// Extract the price
	price := matches[1]
	return price, nil
}

// getUsdToIrrPrice returns USD to IRR price (from cache if available)
func getUsdToIrrPrice() (string, error) {
	priceCache.mutex.RLock()
	cachedPrice := priceCache.UsdToIrr
	lastUpdate := priceCache.LastUpdate
	priceCache.mutex.RUnlock()

	// If we have a valid cached price (non-empty and not too old), use it
	if cachedPrice != "" && time.Since(lastUpdate) < 70*time.Minute {
		return cachedPrice, nil
	}

	// Otherwise fetch fresh data
	return fetchUsdToIrrPrice()
}

// fetchGoldPriceInIRR scrapes the gold price in IRR from tgju.org
func fetchGoldPriceInIRR() (string, error) {
	url := "https://www.tgju.org/gold-chart"

	client := &http.Client{Timeout: 15 * time.Second}

	// Set custom headers to mimic browser request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Read the HTML content
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	bodyString := string(bodyBytes)

	// More general pattern to find gold price
	regex := regexp.MustCompile(`<th>آبشده نقدی</th>\s*<td class="nf">([0-9,]+)</td>`)
	matches := regex.FindStringSubmatch(bodyString)

	if len(matches) < 2 {
		// Try alternative pattern
		regex = regexp.MustCompile(`data-market-nameslug="gold_futures"[^>]*>.*?<td class="nf">([0-9,]+)</td>`)
		matches = regex.FindStringSubmatch(bodyString)

		if len(matches) < 2 {
			// Try another pattern for gold price
			regex = regexp.MustCompile(`class="nf">([0-9,]+)</td>\s*<td class="low">[^<]*</td>[^<]*<td>[^<]*</td>[^<]*<td>[^<]*</td>[^<]*<td>[^<]*</td>`)
			matches = regex.FindStringSubmatch(bodyString)

			if len(matches) < 2 {
				return "", fmt.Errorf("couldn't find gold price in IRR in the webpage")
			}
		}
	}

	// Extract the price
	price := matches[1]
	return price, nil
}

// getGoldPriceInIRR returns Gold price in IRR (from cache if available)
func getGoldPriceInIRR() (string, error) {
	priceCache.mutex.RLock()
	cachedPrice := priceCache.GoldIrr
	lastUpdate := priceCache.LastUpdate
	priceCache.mutex.RUnlock()

	// If we have a valid cached price (non-empty and not too old), use it
	if cachedPrice != "" && time.Since(lastUpdate) < 70*time.Minute {
		return cachedPrice, nil
	}

	// Otherwise fetch fresh data
	return fetchGoldPriceInIRR()
}

// getPriceMessage returns a formatted message with current prices
func getPriceMessage() string {
	bitcoinPrice, btcErr := getBitcoinPrice()
	goldPrice, goldUsdErr := getGoldPrice()
	usdToIrrPrice, usdIrrErr := getUsdToIrrPrice()
	goldIrrPrice, goldIrrErr := getGoldPriceInIRR()

	// Check if we have at least some prices to display
	if btcErr != nil && goldUsdErr != nil && usdIrrErr != nil && goldIrrErr != nil {
		return "❌ *Error*: Could not retrieve any price data. Please try again later."
	}

	var messageBuilder strings.Builder
	messageBuilder.WriteString("📊 *Current Market Prices*\n\n")

	// Global prices section
	messageBuilder.WriteString("🌎 *Global Markets*:\n")
	if btcErr == nil {
		messageBuilder.WriteString(fmt.Sprintf("• *Bitcoin*: $%.2f\n", bitcoinPrice))
	} else {
		messageBuilder.WriteString("• *Bitcoin*: Data unavailable\n")
	}

	if goldUsdErr == nil {
		messageBuilder.WriteString(fmt.Sprintf("• *Gold* (per ounce): $%.2f\n", goldPrice))
	} else {
		messageBuilder.WriteString("• *Gold* (per ounce): Data unavailable\n")
	}

	// Iranian market section
	messageBuilder.WriteString("\n🇮🇷 *Iranian Market*:\n")
	if usdIrrErr == nil {
		messageBuilder.WriteString(fmt.Sprintf("• *USD to IRR*: %s Rials\n", usdToIrrPrice))
	} else {
		messageBuilder.WriteString("• *USD to IRR*: Data unavailable\n")
	}

	if goldIrrErr == nil {
		messageBuilder.WriteString(fmt.Sprintf("• *Gold in IRR*: %s Rials\n", goldIrrPrice))
	} else {
		messageBuilder.WriteString("• *Gold in IRR*: Data unavailable\n")
	}

	// Get the cache update time
	priceCache.mutex.RLock()
	lastUpdate := priceCache.LastUpdate
	priceCache.mutex.RUnlock()

	var updateTimeStr string
	if lastUpdate.IsZero() {
		updateTimeStr = time.Now().Format("2006-01-02 15:04:05")
	} else {
		updateTimeStr = lastUpdate.Format("2006-01-02 15:04:05")
	}

	// Footer with timestamps
	messageBuilder.WriteString(fmt.Sprintf("\n_Cache last updated: %s_", updateTimeStr))

	return messageBuilder.String()
}

// StartScheduler starts the scheduler to send periodic updates
func StartScheduler(bot *tgbotapi.BotAPI, manager *SubscriptionManager) {
	// Create a ticker that checks every minute
	ticker := time.NewTicker(1 * time.Minute)

	go func() {
		for range ticker.C {
			// Check all subscriptions
			for _, sub := range manager.GetAllSubscriptions() {
				// We'll use a simple approach: if the current minute is divisible by the interval
				if time.Now().Minute()%int(sub.Interval.Minutes()) == 0 {
					msg := tgbotapi.NewMessage(sub.ChatID, getPriceMessage())
					msg.ParseMode = "Markdown"

					if _, err := bot.Send(msg); err != nil {
						log.Printf("Error sending scheduled message to %s (ID: %d): %v", sub.ChatTitle, sub.ChatID, err)
					}
				}
			}
		}
	}()
}

// setupLogging configures logging to both console and file
func setupLogging() *os.File {
	// Create logs directory if it doesn't exist
	if err := os.MkdirAll("logs", 0755); err != nil {
		log.Printf("Warning: could not create logs directory: %v", err)
	}

	// Create log file with current date
	logFileName := filepath.Join("logs", fmt.Sprintf("bot_%s.log", time.Now().Format("2006-01-02")))
	logFile, err := os.OpenFile(logFileName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Warning: could not create log file: %v", err)
		return nil
	}

	// Configure log to write to both file and console
	log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Println("Logging configured successfully")

	return logFile
}

func main() {
	// Setup logging
	logFile := setupLogging()
	if logFile != nil {
		defer logFile.Close()
	}

	log.Println("Starting Price Telegram Bot")

	// Start the cache refresher
	log.Println("Starting hourly price cache refresher")
	StartCacheRefresher()

	// Get bot token from environment variable
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN environment variable is not set")
	}

	// Initialize bot
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Fatalf("Error initializing bot: %v", err)
	}

	log.Printf("Authorized on account %s", bot.Self.UserName)

	// Initialize subscription manager
	subManager := NewSubscriptionManager()

	// Start the scheduler
	StartScheduler(bot, subManager)

	// Set up updates configuration
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	// Get updates channel
	updates := bot.GetUpdatesChan(u)

	log.Println("Bot is now running. Press CTRL-C to exit.")

	// Process updates
	for update := range updates {
		if update.Message == nil {
			continue
		}

		// Get chat info
		chatID := update.Message.Chat.ID
		chatTitle := update.Message.Chat.Title
		if chatTitle == "" {
			// If it's a private chat, use the username or first name
			if update.Message.Chat.UserName != "" {
				chatTitle = update.Message.Chat.UserName
			} else {
				chatTitle = update.Message.Chat.FirstName
			}
		}

		// Log the incoming message
		log.Printf("Received message from %s (ID: %d): %s", chatTitle, chatID, update.Message.Text)

		// Check if message is a command
		if update.Message.IsCommand() {
			msg := tgbotapi.NewMessage(chatID, "")
			msg.ParseMode = "Markdown"

			command := update.Message.Command()

			// Handle commands
			switch command {
			case "start":
				msg.Text = "Welcome to Price Bot! Use /price to get Bitcoin and Gold prices in USD, or /subscribe to receive regular updates."
			case "help":
				msg.Text = "*Available commands:*\n" +
					"/price - Get Bitcoin, Gold prices in USD, USD to IRR rate, and Gold price in IRR\n" +
					"/bitcoin - Get Bitcoin price in USD\n" +
					"/gold - Get Gold price in USD\n" +
					"/usd - Get USD to IRR exchange rate\n" +
					"/goldirr - Get Gold price in IRR\n" +
					"/subscribe <minutes> - Subscribe to price updates (e.g. /subscribe 30 for updates every 30 minutes)\n" +
					"/unsubscribe - Stop receiving price updates\n" +
					"/status - Check subscription status\n" +
					"/refresh - Force refresh of price data"
			case "price":
				msg.Text = getPriceMessage()
			case "bitcoin":
				bitcoinPrice, err := getBitcoinPrice()
				if err != nil {
					msg.Text = fmt.Sprintf("Error getting Bitcoin price: %v", err)
					break
				}
				msg.Text = fmt.Sprintf("🔸 *Bitcoin*: $%.2f", bitcoinPrice)
			case "gold":
				goldPrice, err := getGoldPrice()
				if err != nil {
					msg.Text = fmt.Sprintf("Error getting Gold price: %v", err)
					break
				}
				msg.Text = fmt.Sprintf("🔸 *Gold* (per ounce): $%.2f", goldPrice)
			case "usd":
				usdToIrrPrice, err := getUsdToIrrPrice()
				if err != nil {
					msg.Text = fmt.Sprintf("Error getting USD to IRR exchange rate: %v", err)
					break
				}
				msg.Text = fmt.Sprintf("🔸 *USD to IRR*: %s Rials", usdToIrrPrice)
			case "goldirr":
				goldIrrPrice, err := getGoldPriceInIRR()
				if err != nil {
					msg.Text = fmt.Sprintf("Error getting Gold price in IRR: %v", err)
					break
				}
				msg.Text = fmt.Sprintf("🔸 *Gold in IRR*: %s Rials", goldIrrPrice)
			case "refresh":
				go priceCache.refreshCache()
				msg.Text = "🔄 Refreshing price data. This may take a few seconds. Use /price to check the updated data."
			case "subscribe":
				// Default interval is 60 minutes
				interval := 60 * time.Minute

				// Check if user provided a custom interval
				args := update.Message.CommandArguments()
				if args != "" {
					minutes, err := strconv.Atoi(args)
					if err != nil || minutes < 1 {
						msg.Text = "Invalid interval. Please provide a positive number of minutes, e.g., `/subscribe 30`"
						break
					}
					interval = time.Duration(minutes) * time.Minute
				}

				// Subscribe the chat
				subManager.Subscribe(chatID, chatTitle, interval)
				minutes := int(interval.Minutes())
				msg.Text = fmt.Sprintf("✅ This chat will now receive price updates every %d minutes.", minutes)
				log.Printf("Chat %s (ID: %d) subscribed with %d minutes interval", chatTitle, chatID, minutes)

			case "unsubscribe":
				// Unsubscribe the chat
				if subManager.Unsubscribe(chatID) {
					msg.Text = "✅ Unsubscribed. You will no longer receive price updates."
					log.Printf("Chat %s (ID: %d) unsubscribed", chatTitle, chatID)
				} else {
					msg.Text = "ℹ️ This chat is not subscribed to price updates."
				}

			case "status":
				// Check subscription status
				if sub, exists := subManager.GetSubscription(chatID); exists {
					minutes := int(sub.Interval.Minutes())
					msg.Text = fmt.Sprintf("✅ This chat is subscribed to receive price updates every %d minutes.", minutes)
				} else {
					msg.Text = "ℹ️ This chat is not subscribed to price updates. Use /subscribe to start receiving updates."
				}

				// Add cache status information
				priceCache.mutex.RLock()
				lastUpdate := priceCache.LastUpdate
				priceCache.mutex.RUnlock()

				if !lastUpdate.IsZero() {
					msg.Text += fmt.Sprintf("\n\n📊 Price cache last updated: %s", lastUpdate.Format("2006-01-02 15:04:05"))
				} else {
					msg.Text += "\n\n📊 Price cache has not been updated yet."
				}

			default:
				msg.Text = "Unknown command. Use /help to see available commands."
			}

			log.Printf("Responding to /%s command from %s (ID: %d)", command, chatTitle, chatID)

			if _, err := bot.Send(msg); err != nil {
				log.Printf("Error sending message to %s (ID: %d): %v", chatTitle, chatID, err)
			}
		}
	}
}
