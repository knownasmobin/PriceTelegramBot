package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// PriceCache stores cached price data
type PriceCache struct {
	BitcoinUSD float64
	GoldUSD    float64
	UsdToIrr   string
	GoldIrr    string
	GbpToIrr   string
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

func (pc *PriceCache) updateGbpToIrrPrice() error {
	price, err := fetchGbpToIrrPrice()
	if err != nil {
		return err
	}

	pc.mutex.Lock()
	pc.GbpToIrr = price
	pc.mutex.Unlock()
	return nil
}

// refreshCache updates all cached prices
func (pc *PriceCache) refreshCache() {
	log.Println("Refreshing price cache...")

	var wg sync.WaitGroup
	wg.Add(5)

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

	// Update GBP to IRR exchange rate
	go func() {
		defer wg.Done()
		if err := pc.updateGbpToIrrPrice(); err != nil {
			log.Printf("Error updating GBP to IRR exchange rate: %v", err)
		} else {
			log.Println("GBP to IRR exchange rate updated successfully")
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

// getBrowserPath returns the Chrome executable path based on environment
func getBrowserPath() string {
	// First check if CHROME_BIN environment variable is set
	if chromeBin := os.Getenv("CHROME_BIN"); chromeBin != "" {
		if _, err := os.Stat(chromeBin); err == nil {
			log.Printf("Using Chrome binary from CHROME_BIN env var: %s", chromeBin)
			return chromeBin
		}
		log.Printf("Warning: CHROME_BIN env var set to %s but file not found", chromeBin)
	}

	// Check if running in Docker (common environment variable in containers)
	if os.Getenv("DOCKER_CONTAINER") != "" || os.Getenv("CONTAINER_NAME") != "" {
		// Standard location for Chrome/Chromium in Alpine/Debian containers
		paths := []string{
			"/usr/bin/chromium-browser",
			"/usr/bin/chromium",
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
		}

		for _, path := range paths {
			if _, err := os.Stat(path); err == nil {
				log.Printf("Found browser at %s", path)
				return path
			}
		}

		log.Println("No Chrome/Chromium installation found in container!")
	}

	// When not in container, let chromedp find the browser automatically
	return ""
}

// createChromeTempDir creates a unique temporary directory for Chrome
func createChromeTempDir() string {
	// Create a unique directory name using timestamp
	dirName := fmt.Sprintf("chrome_%d", time.Now().UnixNano())
	tempDir := filepath.Join(os.TempDir(), dirName)

	// Create the directory
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		log.Printf("Error creating Chrome temp directory: %v", err)
		return os.TempDir() // Fallback to system temp dir
	}

	return tempDir
}

// cleanupChromeTempDirs removes old Chrome temp directories
func cleanupChromeTempDirs() {
	// Get all directories in temp that start with "chrome_"
	pattern := filepath.Join(os.TempDir(), "chrome_*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		log.Printf("Error finding Chrome temp directories: %v", err)
		return
	}

	// Remove directories older than 1 hour
	for _, dir := range matches {
		info, err := os.Stat(dir)
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > time.Hour {
			os.RemoveAll(dir)
		}
	}
}

// configureProxyOpts adds proxy settings to chromedp options if TGJU_PROXY is set
func configureProxyOpts(opts []chromedp.ExecAllocatorOption) []chromedp.ExecAllocatorOption {
	// Check if the proxy env var is set, even if we don't use its value directly here
	// We use its presence to decide whether to configure PAC
	proxyURLEnv := os.Getenv("TGJU_PROXY")
	log.Printf("Raw TGJU_PROXY env var: '%s' (Used to enable PAC config)", proxyURLEnv)

	if proxyURLEnv != "" {
		// --- Using PAC File Configuration ---
		pacFilePath := "file:///app/proxy.pac" // Absolute path inside the container
		log.Printf("Applying proxy via PAC file: %s", pacFilePath)

		opts = append(opts,
			chromedp.Flag("proxy-pac-url", pacFilePath),
			chromedp.Flag("ignore-certificate-errors", true), // Keep this, useful with proxies
		)

		// Remove potentially conflicting direct proxy flags if they were added before
		// (Defensive coding, though should not happen with current structure)
		var filteredOpts []chromedp.ExecAllocatorOption
		for _, opt := range opts {
			// Check if the option is the --proxy-server flag
			if !strings.HasPrefix(fmt.Sprintf("%#v", opt), "chromedp.Flag(\"proxy-server\",") {
				filteredOpts = append(filteredOpts, opt)
			} else {
				log.Println("Defensive filter: Removing existing --proxy-server flag before adding PAC URL.")
			}
		}
		opts = filteredOpts

	} else {
		log.Println("No TGJU_PROXY env var set, skipping PAC configuration.")
	}
	return opts
}

// configureChromeOptions sets up Chrome options with proper flags
func configureChromeOptions() []chromedp.ExecAllocatorOption {
	// Create a unique temp directory for this Chrome instance
	tempDir := createChromeTempDir()

	// Base options
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		// Container-specific flags
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("headless", true),
		// Cookie and storage settings
		chromedp.Flag("disable-cookies", false),
		chromedp.Flag("disable-storage-reset", true),
		chromedp.Flag("disk-cache-dir", filepath.Join(tempDir, "cache")),
		chromedp.Flag("user-data-dir", tempDir),
		// Prevent process scheduler issues
		chromedp.Flag("disable-process-singleton", true),
		chromedp.Flag("disable-features", "ProcessPerSite,IsolateOrigins,site-per-process"),
		// Memory and performance optimizations
		chromedp.Flag("single-process", true),
		chromedp.Flag("no-zygote", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		// Security settings
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.Flag("allow-insecure-localhost", true),
		// Performance settings
		chromedp.Flag("disable-software-rasterizer", true),
		chromedp.Flag("disable-accelerated-2d-canvas", true),
		chromedp.Flag("disable-accelerated-jpeg-decoding", true),
		chromedp.Flag("disable-accelerated-mjpeg-decode", true),
		chromedp.Flag("disable-accelerated-video-decode", true),
		// Container-specific workarounds
		chromedp.Flag("disable-features", "TranslateUI,BlinkGenPropertyTrees"),
		chromedp.Flag("disable-breakpad", true),
		chromedp.Flag("disable-component-extensions-with-background-pages", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("metrics-recording-only", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("no-pings", true),
		chromedp.Flag("no-sandbox-and-elevated", true),
		chromedp.Flag("no-service-autorun", true),
		chromedp.Flag("no-wifi", true),
		chromedp.Flag("password-store", "basic"),
		chromedp.Flag("use-mock-keychain", true),
	)

	// Add proxy configuration if available
	opts = configureProxyOpts(opts)

	return opts
}

// fetchUsdToIrrPrice fetches the USD to IRR price, trying Mazaneh first, then Bonbast.
func fetchUsdToIrrPrice() (string, error) {
	// Try Mazaneh first
	usdPrice, _, _, err := fetchMazanehPrices()
	// Handle error OR empty price from Mazaneh
	if err == nil && usdPrice != "" {
		log.Println("Using USD/IRR price from mazaneh.net")
		return usdPrice, nil
	}
	if err != nil {
		log.Printf("Failed to fetch prices from mazaneh.net, falling back: %v", err)
	} else { // err == nil but usdPrice == ""
		log.Printf("Fetched from mazaneh.net but USD price was empty, falling back.")
	}

	// Fallback 1: Try fetching with browser (bonbast)
	log.Println("Falling back to fetching USD/IRR from Bonbast (browser)...")
	price, err := fetchUsdToIrrWithBrowser()
	if err != nil {
		log.Printf("Bonbast browser fetch failed: %v", err)
		// Fallback 2: Try fallback method (bonbast simple GET)
		log.Println("Falling back to fetching USD/IRR from Bonbast (fallback GET)...")
		return fetchUsdToIrrFallback()
	}
	return price, nil
}

// fetchUsdToIrrWithBrowser uses chromedp to fetch USD to IRR price with a headless browser
func fetchUsdToIrrWithBrowser() (string, error) {
	// Create a context with a timeout for the entire operation
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Create and clean up Chrome temp directory
	chromeTemDir := createChromeTempDir()
	cleanupChromeTempDirs()

	// Set the TMPDIR environment variable for Chrome
	os.Setenv("TMPDIR", chromeTemDir)

	// Base chromedp options - keeping the original options
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("single-process", true),
		chromedp.Flag("no-zygote", true),
		chromedp.Flag("deterministic-fetch", true),
		// Reduce memory and disk usage
		chromedp.Flag("aggressive-cache-discard", true),
		chromedp.Flag("disable-cache", true),
		chromedp.Flag("disable-application-cache", true),
		chromedp.Flag("disable-offline-load-stale-cache", true),
		chromedp.Flag("disable-extensions", true),
		// Set custom temp directory via command line as well
		chromedp.Flag("disk-cache-dir", filepath.Join(chromeTemDir, "cache")),
		chromedp.Flag("homedir", chromeTemDir),
		// Make browser look more like a regular browser
		chromedp.Flag("window-size", "1920,1080"),
		chromedp.Flag("start-maximized", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-features", "IsolateOrigins,site-per-process"),
	)

	// Add proxy configuration if available
	opts = configureProxyOpts(opts)

	// Set User Agent last
	opts = append(opts, chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"))

	// Add Chrome path if available
	if browserPath := getBrowserPath(); browserPath != "" {
		opts = append(opts, chromedp.ExecPath(browserPath))
		log.Printf("Using browser at path: %s", browserPath)
	} else {
		log.Println("No specific browser path set, letting chromedp find browser automatically")
	}

	// Create browser context with error handling
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	// Create browser context with custom timeout for browser startup
	browserCtx, browserCancel := context.WithTimeout(allocCtx, 60*time.Second)
	defer browserCancel()

	taskCtx, taskCancel := chromedp.NewContext(
		browserCtx,
		chromedp.WithLogf(log.Printf),
		chromedp.WithErrorf(log.Printf),
	)
	defer taskCancel()

	// Try a simple navigation to ensure browser starts correctly
	if err := chromedp.Run(taskCtx, chromedp.Navigate("about:blank")); err != nil {
		log.Printf("ERROR initializing browser: %v", err)
		return "", fmt.Errorf("failed to initialize browser: %v", err)
	}

	var price string
	var htmlContent string

	// Navigate to the page and extract price
	err := chromedp.Run(taskCtx,
		chromedp.Navigate("https://mazaneh.net/fa"),
		chromedp.Sleep(5*time.Second),
		chromedp.Evaluate(`document.documentElement.outerHTML`, &htmlContent),
		chromedp.ActionFunc(func(ctx context.Context) error {
			log.Println("Page loaded, searching for price element...")
			return nil
		}),
		chromedp.Text(`div#USD .CurrencyPrice`, &price, chromedp.ByQuery, chromedp.NodeVisible),
	)

	if err != nil {
		if strings.Contains(err.Error(), "net::ERR") && os.Getenv("TGJU_PROXY") != "" {
			log.Printf("⚠️ PROXY ERROR suspected: Could not navigate: %v", err)
			return "", fmt.Errorf("proxy connection failed or navigation error: %v", err)
		}

		// Try regex as fallback if we have HTML content
		if htmlContent != "" {
			regex := regexp.MustCompile(`<div id="USD" class="currencyShape">.*?<div class="CurrencyPrice">([0-9,]+)</div>`)
			matches := regex.FindStringSubmatch(htmlContent)
			if len(matches) >= 2 {
				price = matches[1]
				log.Printf("Successfully extracted USD price via regex: %s", price)
				return price, nil
			}
		}

		return "", fmt.Errorf("failed to navigate or find price element: %v", err)
	}

	if price == "" {
		// Try regex as fallback
		regex := regexp.MustCompile(`<div id="USD" class="currencyShape">.*?<div class="CurrencyPrice">([0-9,]+)</div>`)
		matches := regex.FindStringSubmatch(htmlContent)
		if len(matches) >= 2 {
			price = matches[1]
		} else {
			return "", fmt.Errorf("couldn't find USD to IRR price")
		}
	}

	log.Printf("Successfully extracted USD to IRR price: %s", price)
	return price, nil
}

// fetchUsdToIrrFallback tries to fetch USD to IRR price using HTTP requests without a browser
func fetchUsdToIrrFallback() (string, error) {
	log.Println("Using HTTP fallback method to fetch USD to IRR price...")

	// Try multiple approaches
	urls := []string{
		"https://mazaneh.net/fa",
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Check if a proxy is configured
	if proxyURL := os.Getenv("TGJU_PROXY"); proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err == nil {
			transport := &http.Transport{
				Proxy: http.ProxyURL(proxy),
			}
			client.Transport = transport
			log.Printf("Using proxy for HTTP fallback: %s", proxyURL)
		}
	}

	// Try each URL
	for _, urlStr := range urls {
		req, err := http.NewRequest("GET", urlStr, nil)
		if err != nil {
			continue
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.5")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Request to %s failed: %v", urlStr, err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("Failed to read response from %s: %v", urlStr, err)
			continue
		}

		// Try different regex patterns to extract the price
		patterns := []string{
			`<div id="USD" class="currencyShape">.*?<div class="CurrencyPrice">([0-9,]+)</div>`,
			`<div class="EnglishTitle">USD</div>.*?<div class="CurrencyPrice">([0-9,]+)</div>`,
		}

		for _, pattern := range patterns {
			regex := regexp.MustCompile(pattern)
			matches := regex.FindStringSubmatch(string(body))
			if len(matches) >= 2 {
				log.Printf("USD to IRR price found with fallback method: %s", matches[1])
				return matches[1], nil
			}
		}
	}

	return "", fmt.Errorf("all fallback methods failed to fetch USD to IRR price")
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

// fetchGoldPriceInIRR fetches the Gold price in IRR, trying Mazaneh first, then Bonbast.
func fetchGoldPriceInIRR() (string, error) {
	// Try Mazaneh first
	_, goldPrice, _, err := fetchMazanehPrices()
	// Handle error OR empty price from Mazaneh
	if err == nil && goldPrice != "" {
		log.Println("Using Gold/IRR price from mazaneh.net")
		return goldPrice, nil
	}
	if err != nil {
		log.Printf("Failed to fetch prices from mazaneh.net, falling back: %v", err)
	} else { // err == nil but goldPrice == ""
		log.Printf("Fetched from mazaneh.net but Gold price was empty, falling back.")
	}

	// Fallback 1: Try fetching with browser (bonbast)
	log.Println("Falling back to fetching Gold/IRR from Bonbast (browser)...")
	price, err := fetchGoldIrrWithBrowser()
	if err != nil {
		log.Printf("Bonbast browser fetch failed: %v", err)
		// Fallback 2: Try fallback method (bonbast simple GET)
		log.Println("Falling back to fetching Gold/IRR from Bonbast (fallback GET)...")
		return fetchGoldIrrFallback()
	}
	return price, nil
}

// fetchGoldIrrWithBrowser uses chromedp to fetch Gold price in IRR with a headless browser
func fetchGoldIrrWithBrowser() (string, error) {
	// Create a context with a timeout for the entire operation
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Create and clean up Chrome temp directory
	chromeTemDir := createChromeTempDir()
	cleanupChromeTempDirs()

	// Set the TMPDIR environment variable for Chrome
	os.Setenv("TMPDIR", chromeTemDir)

	// Base chromedp options - keeping the original options
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("single-process", true),
		chromedp.Flag("no-zygote", true),
		chromedp.Flag("deterministic-fetch", true),
		// Reduce memory and disk usage
		chromedp.Flag("aggressive-cache-discard", true),
		chromedp.Flag("disable-cache", true),
		chromedp.Flag("disable-application-cache", true),
		chromedp.Flag("disable-offline-load-stale-cache", true),
		chromedp.Flag("disable-extensions", true),
		// Set custom temp directory via command line as well
		chromedp.Flag("disk-cache-dir", filepath.Join(chromeTemDir, "cache")),
		chromedp.Flag("homedir", chromeTemDir),
		// Make browser look more like a regular browser
		chromedp.Flag("window-size", "1920,1080"),
		chromedp.Flag("start-maximized", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-features", "IsolateOrigins,site-per-process"),
	)

	// Add proxy configuration if available
	opts = configureProxyOpts(opts)

	// Set User Agent last
	opts = append(opts, chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"))

	// Add Chrome path if available
	if browserPath := getBrowserPath(); browserPath != "" {
		opts = append(opts, chromedp.ExecPath(browserPath))
		log.Printf("Using browser at path: %s", browserPath)
	} else {
		log.Println("No specific browser path set, letting chromedp find browser automatically")
	}

	// Create browser context with error handling
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	// Create browser context with custom timeout for browser startup
	browserCtx, browserCancel := context.WithTimeout(allocCtx, 60*time.Second)
	defer browserCancel()

	taskCtx, taskCancel := chromedp.NewContext(
		browserCtx,
		chromedp.WithLogf(log.Printf),
		chromedp.WithErrorf(log.Printf),
	)
	defer taskCancel()

	// Try a simple navigation to ensure browser starts correctly
	if err := chromedp.Run(taskCtx, chromedp.Navigate("about:blank")); err != nil {
		log.Printf("ERROR initializing browser: %v", err)
		return "", fmt.Errorf("failed to initialize browser: %v", err)
	}

	var price string
	var htmlContent string

	// Navigate to the page and extract price
	err := chromedp.Run(taskCtx,
		chromedp.Navigate("https://mazaneh.net/fa"),
		chromedp.Sleep(5*time.Second),
		chromedp.Evaluate(`document.documentElement.outerHTML`, &htmlContent),
		chromedp.ActionFunc(func(ctx context.Context) error {
			log.Println("Page loaded, searching for price element...")
			return nil
		}),
		chromedp.Text(`div#Div38 .CurrencyPrice`, &price, chromedp.ByQuery, chromedp.NodeVisible),
	)

	if err != nil {
		if strings.Contains(err.Error(), "net::ERR") && os.Getenv("TGJU_PROXY") != "" {
			log.Printf("⚠️ PROXY ERROR suspected: Could not navigate: %v", err)
			return "", fmt.Errorf("proxy connection failed or navigation error: %v", err)
		}

		// Try regex as fallback if we have HTML content
		if htmlContent != "" {
			regex := regexp.MustCompile(`<div id="Div38" class="currencyShape">.*?<div class="CurrencyPrice">([0-9,]+)</div>`)
			matches := regex.FindStringSubmatch(htmlContent)
			if len(matches) >= 2 {
				price = matches[1]
				log.Printf("Successfully extracted Gold price via regex: %s", price)
				return price, nil
			}
		}

		return "", fmt.Errorf("failed to navigate or find price element: %v", err)
	}

	if price == "" {
		// Try regex as fallback
		regex := regexp.MustCompile(`<div id="Div38" class="currencyShape">.*?<div class="CurrencyPrice">([0-9,]+)</div>`)
		matches := regex.FindStringSubmatch(htmlContent)
		if len(matches) >= 2 {
			price = matches[1]
		} else {
			return "", fmt.Errorf("couldn't find Gold price in IRR")
		}
	}

	log.Printf("Successfully extracted Gold price in IRR: %s", price)
	return price, nil
}

// fetchGoldIrrFallback tries to fetch Gold IRR price using HTTP requests without a browser
func fetchGoldIrrFallback() (string, error) {
	log.Println("Using HTTP fallback method to fetch Gold IRR price...")

	// Try multiple approaches
	urls := []string{
		"https://mazaneh.net/fa",
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Check if a proxy is configured
	if proxyURL := os.Getenv("TGJU_PROXY"); proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err == nil {
			transport := &http.Transport{
				Proxy: http.ProxyURL(proxy),
			}
			client.Transport = transport
			log.Printf("Using proxy for HTTP fallback: %s", proxyURL)
		}
	}

	// Try each URL
	for _, urlStr := range urls {
		req, err := http.NewRequest("GET", urlStr, nil)
		if err != nil {
			continue
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.5")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Request to %s failed: %v", urlStr, err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("Failed to read response from %s: %v", urlStr, err)
			continue
		}

		// Try different regex patterns to extract the price
		patterns := []string{
			`<div id="Div38" class="currencyShape">.*?<div class="CurrencyPrice">([0-9,]+)</div>`,
			`<div class="EnglishTitle">Gold</div>.*?<div class="CurrencyPrice">([0-9,]+)</div>`,
		}

		for _, pattern := range patterns {
			regex := regexp.MustCompile(pattern)
			matches := regex.FindStringSubmatch(string(body))
			if len(matches) >= 2 {
				log.Printf("Gold IRR price found with fallback method: %s", matches[1])
				return matches[1], nil
			}
		}
	}

	return "", fmt.Errorf("all fallback methods failed to fetch Gold IRR price")
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

// fetchGbpToIrrPrice fetches the GBP to IRR price, trying Mazaneh first, then Bonbast.
func fetchGbpToIrrPrice() (string, error) {
	// Try Mazaneh first
	_, _, gbpPrice, err := fetchMazanehPrices()
	// Handle error OR empty price from Mazaneh
	if err == nil && gbpPrice != "" {
		log.Println("Using GBP/IRR price from mazaneh.net")
		return gbpPrice, nil
	}
	if err != nil {
		log.Printf("Failed to fetch prices from mazaneh.net, falling back to Bonbast: %v", err)
	} else { // err == nil but gbpPrice == ""
		log.Printf("Fetched from mazaneh.net but GBP price was empty, falling back to Bonbast.")
	}

	// Fallback 1: Try fetching with browser (bonbast)
	log.Println("Falling back to fetching GBP/IRR from Bonbast (browser)...")
	price, err := fetchGbpToIrrWithBrowser() // Call the existing Bonbast browser func
	if err != nil {
		log.Printf("Bonbast browser fetch failed: %v", err)
		// Fallback 2: Try fallback method (bonbast simple GET)
		log.Println("Falling back to fetching GBP/IRR from Bonbast (fallback GET)...")
		return fetchGbpToIrrFallback() // Call the existing Bonbast fallback func
	}
	return price, nil
}

// fetchGbpToIrrWithBrowser uses chromedp to fetch GBP price in IRR with a headless browser
func fetchGbpToIrrWithBrowser() (string, error) {
	// Create a context with a timeout for the entire operation
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Create and clean up Chrome temp directory
	chromeTemDir := createChromeTempDir()
	cleanupChromeTempDirs()

	// Set the TMPDIR environment variable for Chrome
	os.Setenv("TMPDIR", chromeTemDir)

	// Base chromedp options - keeping the original options
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("single-process", true),
		chromedp.Flag("no-zygote", true),
		chromedp.Flag("deterministic-fetch", true),
		// Reduce memory and disk usage
		chromedp.Flag("aggressive-cache-discard", true),
		chromedp.Flag("disable-cache", true),
		chromedp.Flag("disable-application-cache", true),
		chromedp.Flag("disable-offline-load-stale-cache", true),
		chromedp.Flag("disable-extensions", true),
		// Set custom temp directory via command line as well
		chromedp.Flag("disk-cache-dir", filepath.Join(chromeTemDir, "cache")),
		chromedp.Flag("homedir", chromeTemDir),
		// Make browser look more like a regular browser
		chromedp.Flag("window-size", "1920,1080"),
		chromedp.Flag("start-maximized", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-features", "IsolateOrigins,site-per-process"),
	)

	// Add proxy configuration if available
	opts = configureProxyOpts(opts)

	// Set User Agent last
	opts = append(opts, chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"))

	// Add Chrome path if available
	if browserPath := getBrowserPath(); browserPath != "" {
		opts = append(opts, chromedp.ExecPath(browserPath))
		log.Printf("Using browser at path: %s", browserPath)
	} else {
		log.Println("No specific browser path set, letting chromedp find browser automatically")
	}

	// Create browser context with error handling
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	// Create browser context with custom timeout for browser startup
	browserCtx, browserCancel := context.WithTimeout(allocCtx, 60*time.Second)
	defer browserCancel()

	taskCtx, taskCancel := chromedp.NewContext(
		browserCtx,
		chromedp.WithLogf(log.Printf),
		chromedp.WithErrorf(log.Printf),
	)
	defer taskCancel()

	// Try a simple navigation to ensure browser starts correctly
	if err := chromedp.Run(taskCtx, chromedp.Navigate("about:blank")); err != nil {
		log.Printf("ERROR initializing browser: %v", err)
		return "", fmt.Errorf("failed to initialize browser: %v", err)
	}

	var price string
	var htmlContent string

	// Navigate to the page and extract price
	err := chromedp.Run(taskCtx,
		chromedp.Navigate("https://mazaneh.net/fa"),
		chromedp.Sleep(5*time.Second),
		chromedp.Evaluate(`document.documentElement.outerHTML`, &htmlContent),
		chromedp.ActionFunc(func(ctx context.Context) error {
			log.Println("Page loaded, searching for price element...")
			return nil
		}),
		chromedp.Text(`div#Div3 .CurrencyPrice`, &price, chromedp.ByQuery, chromedp.NodeVisible),
	)

	if err != nil {
		if strings.Contains(err.Error(), "net::ERR") && os.Getenv("TGJU_PROXY") != "" {
			log.Printf("⚠️ PROXY ERROR suspected: Could not navigate: %v", err)
			return "", fmt.Errorf("proxy connection failed or navigation error: %v", err)
		}

		// Try regex as fallback if we have HTML content
		if htmlContent != "" {
			regex := regexp.MustCompile(`<div id="Div3" class="currencyShape">.*?<div class="CurrencyPrice">([0-9,]+)</div>`)
			matches := regex.FindStringSubmatch(htmlContent)
			if len(matches) >= 2 {
				price = matches[1]
				log.Printf("Successfully extracted GBP price via regex: %s", price)
				return price, nil
			}
		}

		return "", fmt.Errorf("failed to navigate or find price element: %v", err)
	}

	if price == "" {
		// Try regex as fallback
		regex := regexp.MustCompile(`<div id="Div3" class="currencyShape">.*?<div class="CurrencyPrice">([0-9,]+)</div>`)
		matches := regex.FindStringSubmatch(htmlContent)
		if len(matches) >= 2 {
			price = matches[1]
		} else {
			return "", fmt.Errorf("couldn't find GBP price in IRR")
		}
	}

	log.Printf("Successfully extracted GBP price in IRR: %s", price)
	return price, nil
}

// fetchGbpToIrrFallback tries to fetch GBP IRR price using HTTP requests without a browser
func fetchGbpToIrrFallback() (string, error) {
	log.Println("Using HTTP fallback method to fetch GBP IRR price...")

	// Try multiple approaches
	urls := []string{
		"https://mazaneh.net/fa",
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Check if a proxy is configured
	if proxyURL := os.Getenv("TGJU_PROXY"); proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err == nil {
			transport := &http.Transport{
				Proxy: http.ProxyURL(proxy),
			}
			client.Transport = transport
			log.Printf("Using proxy for HTTP fallback: %s", proxyURL)
		}
	}

	// Try each URL
	for _, urlStr := range urls {
		req, err := http.NewRequest("GET", urlStr, nil)
		if err != nil {
			continue
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.5")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Request to %s failed: %v", urlStr, err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("Failed to read response from %s: %v", urlStr, err)
			continue
		}

		// Try different regex patterns to extract the price
		patterns := []string{
			`<div id="Div3" class="currencyShape">.*?<div class="CurrencyPrice">([0-9,]+)</div>`,
			`<div class="EnglishTitle">GBP</div>.*?<div class="CurrencyPrice">([0-9,]+)</div>`,
		}

		for _, pattern := range patterns {
			regex := regexp.MustCompile(pattern)
			matches := regex.FindStringSubmatch(string(body))
			if len(matches) >= 2 {
				log.Printf("GBP IRR price found with fallback method: %s", matches[1])
				return matches[1], nil
			}
		}
	}

	return "", fmt.Errorf("all fallback methods failed to fetch GBP IRR price")
}

// getGbpToIrrPrice returns the latest GBP to IRR exchange rate from the cache
func getGbpToIrrPrice() (string, error) {
	priceCache.mutex.RLock()
	// Check if cache has a valid, non-stale price
	if priceCache.GbpToIrr != "" && !priceCache.LastUpdate.IsZero() && time.Since(priceCache.LastUpdate) < 70*time.Minute {
		price := priceCache.GbpToIrr
		priceCache.mutex.RUnlock()
		return price, nil
	}
	priceCache.mutex.RUnlock() // Release lock before potentially lengthy update

	// Cache miss or stale, trigger update and return fresh/stale/error
	log.Println("Cache miss or stale for GBP/IRR, attempting refresh...")
	err := priceCache.updateGbpToIrrPrice() // This blocks until updated

	priceCache.mutex.RLock() // Re-acquire lock to read potentially updated value
	defer priceCache.mutex.RUnlock()

	if err != nil {
		log.Printf("Error refreshing GBP/IRR price: %v", err)
		// Return stale data if available, otherwise error
		if priceCache.GbpToIrr != "" {
			log.Println("Returning stale GBP/IRR data due to refresh error.")
			return priceCache.GbpToIrr, nil // Return stale data
		}
		return "", fmt.Errorf("failed to fetch GBP/IRR price and no cached value available: %w", err)
	}

	// Refresh succeeded (or was already up-to-date), return the value
	if priceCache.GbpToIrr == "" {
		// Should not happen if update succeeded without error, but check anyway
		return "", fmt.Errorf("GBP/IRR price not available in cache even after refresh attempt")
	}
	return priceCache.GbpToIrr, nil
}

// fetchMazanehPrices fetches USD, Gold (IRT), and GBP prices from mazaneh.net
func fetchMazanehPrices() (usdIrt, goldIrt, gbpIrt string, err error) {
	log.Println("Attempting to fetch prices from mazaneh.net...")

	// Create a context with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Configure Chrome options
	opts := configureChromeOptions()

	// Create allocator context with retry logic
	var allocCtx context.Context
	var allocCancel context.CancelFunc
	var taskCtx context.Context
	var taskCancel context.CancelFunc

	// Retry logic for Chrome startup
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		allocCtx, allocCancel = chromedp.NewExecAllocator(ctx, opts...)
		taskCtx, taskCancel = chromedp.NewContext(allocCtx, chromedp.WithLogf(log.Printf))

		// Try a simple navigation to ensure browser starts correctly
		if err := chromedp.Run(taskCtx, chromedp.Navigate("about:blank")); err == nil {
			break
		}

		// Cleanup failed attempt
		taskCancel()
		allocCancel()

		if i < maxRetries-1 {
			log.Printf("Chrome startup attempt %d failed, retrying...", i+1)
			time.Sleep(2 * time.Second)
		}
	}

	defer allocCancel()
	defer taskCancel()

	// Navigate and extract prices with retry logic
	var htmlContent string
	for i := 0; i < maxRetries; i++ {
		err = chromedp.Run(taskCtx,
			chromedp.Navigate("https://mazaneh.net/fa"),
			chromedp.WaitVisible(`body`),
			chromedp.Sleep(4*time.Second),
			chromedp.Evaluate(`document.documentElement.outerHTML`, &htmlContent),
		)

		if err == nil {
			break
		}

		log.Printf("Navigation attempt %d failed: %v", i+1, err)
		if i < maxRetries-1 {
			time.Sleep(2 * time.Second)
		}
	}

	if err != nil {
		return "", "", "", fmt.Errorf("failed to navigate to mazaneh.net: %v", err)
	}

	// Extract prices using regex as fallback
	usdRegex := regexp.MustCompile(`<div id="USD" class="currencyShape">.*?<div class="CurrencyPrice">([0-9,]+)</div>`)
	goldRegex := regexp.MustCompile(`<div id="Div38" class="currencyShape">.*?<div class="CurrencyPrice">([0-9,]+)</div>`)
	gbpRegex := regexp.MustCompile(`<div id="Div3" class="currencyShape">.*?<div class="CurrencyPrice">([0-9,]+)</div>`)

	usdMatches := usdRegex.FindStringSubmatch(htmlContent)
	goldMatches := goldRegex.FindStringSubmatch(htmlContent)
	gbpMatches := gbpRegex.FindStringSubmatch(htmlContent)

	if len(usdMatches) >= 2 {
		usdIrt = usdMatches[1]
	}
	if len(goldMatches) >= 2 {
		goldIrt = goldMatches[1]
	}
	if len(gbpMatches) >= 2 {
		gbpIrt = gbpMatches[1]
	}

	// Clean up old temp directories
	go cleanupChromeTempDirs()

	return usdIrt, goldIrt, gbpIrt, nil
}

// getPriceMessage returns a formatted message with current prices
func getPriceMessage() string {
	bitcoinPrice, btcErr := getBitcoinPrice()
	goldPrice, goldUsdErr := getGoldPrice()
	usdToIrrPrice, usdIrrErr := getUsdToIrrPrice()
	goldIrrPrice, goldIrrErr := getGoldPriceInIRR()
	gbpToIrrPrice, gbpIrrErr := getGbpToIrrPrice()

	// Check if we have at least some prices to display
	if btcErr != nil && goldUsdErr != nil && usdIrrErr != nil && goldIrrErr != nil && gbpIrrErr != nil {
		return "❌ *Error*: Could not retrieve any price data. Please try again later."
	}

	var messageBuilder strings.Builder
	messageBuilder.WriteString("📊 *Current Market Prices*\n\n")

	// Global prices section
	messageBuilder.WriteString("🌎 *Global Markets*:\n")
	if btcErr == nil {
		messageBuilder.WriteString(fmt.Sprintf("• *Bitcoin*: %.2f USD\n", bitcoinPrice))
	} else {
		messageBuilder.WriteString("• *Bitcoin*: Data unavailable\n")
	}

	if goldUsdErr == nil {
		messageBuilder.WriteString(fmt.Sprintf("• *Gold* (per ounce): %.2f USD\n", goldPrice))
	} else {
		messageBuilder.WriteString("• *Gold* (per ounce): Data unavailable\n")
	}

	// Iranian market section
	messageBuilder.WriteString("\n🇮🇷 *Iranian Market*:\n")
	if usdIrrErr == nil {
		messageBuilder.WriteString(fmt.Sprintf("• *USD to IRT*: %s Tomans\n", usdToIrrPrice))
	} else {
		messageBuilder.WriteString("• *USD to IRT*: Data unavailable\n")
	}

	if gbpIrrErr == nil {
		messageBuilder.WriteString(fmt.Sprintf("• *GBP to IRT*: %s Tomans\n", gbpToIrrPrice))
	} else {
		messageBuilder.WriteString("• *GBP to IRT*: Data unavailable\n")
	}

	if goldIrrErr == nil {
		messageBuilder.WriteString(fmt.Sprintf("• *Gold in IRT*: %s Tomans\n", goldIrrPrice))
	} else {
		messageBuilder.WriteString("• *Gold in IRT*: Data unavailable\n")
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
	messageBuilder.WriteString(fmt.Sprintf("\n_Cache last updated: %s_ GMT", updateTimeStr))

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

// createCommandKeyboard creates an inline keyboard with command buttons
func createCommandKeyboard() tgbotapi.ReplyKeyboardMarkup {
	keyboard := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("📊 Prices"),
			tgbotapi.NewKeyboardButton("💰 Bitcoin"),
			tgbotapi.NewKeyboardButton("🥇 Gold"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("💵 USD/IRT"),
			tgbotapi.NewKeyboardButton("💷 GBP/IRT"),
			tgbotapi.NewKeyboardButton("🏅 Gold/IRT"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("🔄 Refresh"),
			tgbotapi.NewKeyboardButton("ℹ️ Status"),
			tgbotapi.NewKeyboardButton("❓ Help"),
		),
	)
	keyboard.ResizeKeyboard = true
	return keyboard
}

// handleCallbackQuery processes callback queries from inline keyboard buttons
func handleCallbackQuery(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, subManager *SubscriptionManager) {
	chatID := query.Message.Chat.ID
	chatTitle := query.Message.Chat.Title
	if chatTitle == "" {
		if query.Message.Chat.UserName != "" {
			chatTitle = query.Message.Chat.UserName
		} else {
			chatTitle = query.Message.Chat.FirstName
		}
	}

	msg := tgbotapi.NewMessage(chatID, "")
	msg.ParseMode = "Markdown"

	// Handle the callback data (which matches our command names)
	switch query.Data {
	case "price":
		// Run a scraper refresh in the background
		go priceCache.refreshCache()

		// Send a temporary message while refreshing
		tempMsg := tgbotapi.NewMessage(chatID, "⏳ *Fetching latest price data...*")
		tempMsg.ParseMode = "Markdown"
		sentMsg, err := bot.Send(tempMsg)

		// Wait a bit for data to refresh
		time.Sleep(2 * time.Second)

		// Get the updated price message
		msg.Text = getPriceMessage()

		// If we successfully sent the temp message, edit it instead of sending a new one
		if err == nil {
			editMsg := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, msg.Text)
			editMsg.ParseMode = "Markdown"
			if _, err := bot.Send(editMsg); err != nil {
				log.Printf("Error editing message: %v, sending new message instead", err)
				if _, err := bot.Send(msg); err != nil {
					log.Printf("Error sending message to %s (ID: %d): %v", chatTitle, chatID, err)
				}
			}
			return
		}

	case "bitcoin":
		// Refresh Bitcoin price in the background
		go priceCache.updateBitcoinPrice()

		// Send a temporary message while refreshing
		tempMsg := tgbotapi.NewMessage(chatID, "⏳ *Fetching latest Bitcoin price...*")
		tempMsg.ParseMode = "Markdown"
		sentMsg, err := bot.Send(tempMsg)

		// Wait a bit for data to refresh
		time.Sleep(1 * time.Second)

		// Get the updated Bitcoin price
		bitcoinPrice, priceErr := getBitcoinPrice()
		if priceErr != nil {
			msg.Text = fmt.Sprintf("Error getting Bitcoin price: %v", priceErr)
		} else {
			msg.Text = fmt.Sprintf("🔸 *Bitcoin*: %.2f USD", bitcoinPrice)
		}

		// If we successfully sent the temp message, edit it
		if err == nil {
			editMsg := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, msg.Text)
			editMsg.ParseMode = "Markdown"
			if _, err := bot.Send(editMsg); err != nil {
				log.Printf("Error editing message: %v, sending new message instead", err)
				if _, err := bot.Send(msg); err != nil {
					log.Printf("Error sending message to %s (ID: %d): %v", chatTitle, chatID, err)
				}
			}
			return
		}

	case "gold":
		// Refresh Gold price in the background
		go priceCache.updateGoldPrice()

		// Send a temporary message while refreshing
		tempMsg := tgbotapi.NewMessage(chatID, "⏳ *Fetching latest Gold price...*")
		tempMsg.ParseMode = "Markdown"
		sentMsg, err := bot.Send(tempMsg)

		// Wait a bit for data to refresh
		time.Sleep(1 * time.Second)

		// Get the updated Gold price
		goldPrice, priceErr := getGoldPrice()
		if priceErr != nil {
			msg.Text = fmt.Sprintf("Error getting Gold price: %v", priceErr)
		} else {
			msg.Text = fmt.Sprintf("🔸 *Gold* (per ounce): %.2f USD", goldPrice)
		}

		// If we successfully sent the temp message, edit it
		if err == nil {
			editMsg := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, msg.Text)
			editMsg.ParseMode = "Markdown"
			if _, err := bot.Send(editMsg); err != nil {
				log.Printf("Error editing message: %v, sending new message instead", err)
				if _, err := bot.Send(msg); err != nil {
					log.Printf("Error sending message to %s (ID: %d): %v", chatTitle, chatID, err)
				}
			}
			return
		}

	case "usd":
		// Refresh USD to IRR price in the background
		go priceCache.updateUsdToIrrPrice()

		// Send a temporary message while refreshing
		tempMsg := tgbotapi.NewMessage(chatID, "⏳ *Fetching latest USD to IRT exchange rate...*")
		tempMsg.ParseMode = "Markdown"
		sentMsg, err := bot.Send(tempMsg)

		// Wait a bit for data to refresh
		time.Sleep(1 * time.Second)

		// Get the updated USD to IRR price
		usdToIrrPrice, priceErr := getUsdToIrrPrice()
		if priceErr != nil {
			msg.Text = fmt.Sprintf("Error getting USD to IRT exchange rate: %v", priceErr)
		} else {
			msg.Text = fmt.Sprintf("🔸 *USD to IRT*: %s Tomans", usdToIrrPrice)
		}

		// If we successfully sent the temp message, edit it
		if err == nil {
			editMsg := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, msg.Text)
			editMsg.ParseMode = "Markdown"
			if _, err := bot.Send(editMsg); err != nil {
				log.Printf("Error editing message: %v, sending new message instead", err)
				if _, err := bot.Send(msg); err != nil {
					log.Printf("Error sending message to %s (ID: %d): %v", chatTitle, chatID, err)
				}
			}
			return
		}

	case "goldirr":
		// Refresh Gold IRR price in the background
		go priceCache.updateGoldIrrPrice()

		// Send a temporary message while refreshing
		tempMsg := tgbotapi.NewMessage(chatID, "⏳ *Fetching latest Gold price in IRT...*")
		tempMsg.ParseMode = "Markdown"
		sentMsg, err := bot.Send(tempMsg)

		// Wait a bit for data to refresh
		time.Sleep(1 * time.Second)

		// Get the updated Gold IRR price
		goldIrrPrice, priceErr := getGoldPriceInIRR()
		if priceErr != nil {
			msg.Text = fmt.Sprintf("Error getting Gold price in IRT: %v", priceErr)
		} else {
			msg.Text = fmt.Sprintf("🔸 *Gold in IRT*: %s Tomans", goldIrrPrice)
		}

		// If we successfully sent the temp message, edit it
		if err == nil {
			editMsg := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, msg.Text)
			editMsg.ParseMode = "Markdown"
			if _, err := bot.Send(editMsg); err != nil {
				log.Printf("Error editing message: %v, sending new message instead", err)
				if _, err := bot.Send(msg); err != nil {
					log.Printf("Error sending message to %s (ID: %d): %v", chatTitle, chatID, err)
				}
			}
			return
		}

	case "refresh":
		go priceCache.refreshCache()
		msg.Text = "🔄 Refreshing price data. This may take a few seconds. Use /price to check the updated data."

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
			msg.Text += fmt.Sprintf("\n\n📊 Price cache last updated: %s GMT", lastUpdate.Format("2006-01-02 15:04:05"))
		} else {
			msg.Text += "\n\n📊 Price cache has not been updated yet."
		}

	case "help":
		msg.Text = "*Available commands:*\n" +
			"📊 Prices - Get Bitcoin, Gold prices in USD, USD to IRT rate, and Gold price in IRT\n" +
			"💰 Bitcoin - Get Bitcoin price in USD\n" +
			"🥇 Gold - Get Gold price in USD\n" +
			"💵 USD/IRT - Get USD to IRT exchange rate\n" +
			"🏅 Gold/IRT - Get Gold price in IRT\n" +
			"/subscribe <minutes> - Subscribe to price updates (e.g. /subscribe 30 for updates every 30 minutes)\n" +
			"/unsubscribe - Stop receiving price updates\n" +
			"ℹ️ Status - Check subscription status\n" +
			"🔄 Refresh - Force refresh of price data"
	}

	// Send the response message
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Error sending message to %s (ID: %d): %v", chatTitle, chatID, err)
	}

	// Answer the callback query to remove the loading state
	callback := tgbotapi.NewCallback(query.ID, "")
	if _, err := bot.Request(callback); err != nil {
		log.Printf("Error answering callback query: %v", err)
	}
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

	// Validate proxy if configured - REMOVED
	// validateProxy(os.Getenv("TGJU_PROXY"), "https://www.tgju.org")

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
		if update.CallbackQuery != nil {
			handleCallbackQuery(bot, update.CallbackQuery, subManager)
			continue
		}

		if update.Message == nil {
			continue
		}

		// Get chat info
		chatID := update.Message.Chat.ID
		chatTitle := update.Message.Chat.Title
		if chatTitle == "" {
			if update.Message.Chat.UserName != "" {
				chatTitle = update.Message.Chat.UserName
			} else {
				chatTitle = update.Message.Chat.FirstName
			}
		}

		// Log the incoming message
		log.Printf("Received message from %s (ID: %d): %s", chatTitle, chatID, update.Message.Text)

		// Create message with default text
		msg := tgbotapi.NewMessage(chatID, "❌ Error: Could not process your request. Please try again later.")
		msg.ParseMode = "Markdown"

		// Handle both commands and button clicks
		command := update.Message.Text
		if update.Message.IsCommand() {
			command = update.Message.Command()
		}

		switch command {
		case "start":
			msg.Text = "Welcome to Price Bot! Use the buttons below to get price information, or /subscribe to receive regular updates."
			msg.ReplyMarkup = createCommandKeyboard()
		case "help", "❓ Help":
			msg.Text = "*Available commands:*\n" +
				"📊 Prices - Get Bitcoin, Gold prices in USD, USD to IRT rate, and Gold price in IRT\n" +
				"💰 Bitcoin - Get Bitcoin price in USD\n" +
				"🥇 Gold - Get Gold price in USD\n" +
				"💵 USD/IRT - Get USD to IRT exchange rate\n" +
				"💷 GBP/IRT - Get GBP to IRT exchange rate\n" +
				"🏅 Gold/IRT - Get Gold price in IRT\n" +
				"/subscribe <minutes> - Subscribe to price updates (e.g. /subscribe 30 for updates every 30 minutes)\n" +
				"/unsubscribe - Stop receiving price updates\n" +
				"ℹ️ Status - Check subscription status\n" +
				"🔄 Refresh - Force refresh of price data"
			msg.ReplyMarkup = createCommandKeyboard()
		case "💷 GBP/IRT":
			// Refresh GBP to IRT price in the background
			go priceCache.updateGbpToIrrPrice()

			// Send a temporary message while refreshing
			tempMsg := tgbotapi.NewMessage(chatID, "⏳ *Fetching latest GBP to IRT exchange rate...*")
			tempMsg.ParseMode = "Markdown"
			sentMsg, err := bot.Send(tempMsg)

			// Wait a bit for data to refresh
			time.Sleep(1 * time.Second)

			// Get the updated GBP to IRT price
			gbpToIrrPrice, priceErr := getGbpToIrrPrice()
			if priceErr != nil {
				msg.Text = fmt.Sprintf("❌ Error getting GBP to IRT exchange rate: %v", priceErr)
			} else {
				msg.Text = fmt.Sprintf("💷 *GBP to IRT*: %s Tomans", gbpToIrrPrice)
			}

			// If we successfully sent the temp message, edit it
			if err == nil {
				editMsg := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, msg.Text)
				editMsg.ParseMode = "Markdown"
				if _, err := bot.Send(editMsg); err != nil {
					log.Printf("Error editing message: %v, sending new message instead", err)
					if _, err := bot.Send(msg); err != nil {
						log.Printf("Error sending message to %s (ID: %d): %v", chatTitle, chatID, err)
					}
				}
				continue
			}
		case "📊 Prices":
			// Run a scraper refresh in the background
			go priceCache.refreshCache()

			// Send a temporary message while refreshing
			tempMsg := tgbotapi.NewMessage(chatID, "⏳ *Fetching latest price data...*")
			tempMsg.ParseMode = "Markdown"
			sentMsg, err := bot.Send(tempMsg)

			// Wait a bit for data to refresh
			time.Sleep(2 * time.Second)

			// Get the updated price message
			msg.Text = getPriceMessage()

			// If we successfully sent the temp message, edit it instead of sending a new one
			if err == nil {
				editMsg := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, msg.Text)
				editMsg.ParseMode = "Markdown"
				if _, err := bot.Send(editMsg); err != nil {
					log.Printf("Error editing message: %v, sending new message instead", err)
					if _, err := bot.Send(msg); err != nil {
						log.Printf("Error sending message to %s (ID: %d): %v", chatTitle, chatID, err)
					}
				}
				continue
			}
		case "💰 Bitcoin":
			// Refresh Bitcoin price in the background
			go priceCache.updateBitcoinPrice()

			// Send a temporary message while refreshing
			tempMsg := tgbotapi.NewMessage(chatID, "⏳ *Fetching latest Bitcoin price...*")
			tempMsg.ParseMode = "Markdown"
			sentMsg, err := bot.Send(tempMsg)

			// Wait a bit for data to refresh
			time.Sleep(1 * time.Second)

			// Get the updated Bitcoin price
			bitcoinPrice, priceErr := getBitcoinPrice()
			if priceErr != nil {
				msg.Text = fmt.Sprintf("Error getting Bitcoin price: %v", priceErr)
			} else {
				msg.Text = fmt.Sprintf("🔸 *Bitcoin*: %.2f USD", bitcoinPrice)
			}

			// If we successfully sent the temp message, edit it
			if err == nil {
				editMsg := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, msg.Text)
				editMsg.ParseMode = "Markdown"
				if _, err := bot.Send(editMsg); err != nil {
					log.Printf("Error editing message: %v, sending new message instead", err)
					if _, err := bot.Send(msg); err != nil {
						log.Printf("Error sending message to %s (ID: %d): %v", chatTitle, chatID, err)
					}
				}
				continue
			}
		case "🥇 Gold":
			// Refresh Gold price in the background
			go priceCache.updateGoldPrice()

			// Send a temporary message while refreshing
			tempMsg := tgbotapi.NewMessage(chatID, "⏳ *Fetching latest Gold price...*")
			tempMsg.ParseMode = "Markdown"
			sentMsg, err := bot.Send(tempMsg)

			// Wait a bit for data to refresh
			time.Sleep(1 * time.Second)

			// Get the updated Gold price
			goldPrice, priceErr := getGoldPrice()
			if priceErr != nil {
				msg.Text = fmt.Sprintf("Error getting Gold price: %v", priceErr)
			} else {
				msg.Text = fmt.Sprintf("🔸 *Gold* (per ounce): %.2f USD", goldPrice)
			}

			// If we successfully sent the temp message, edit it
			if err == nil {
				editMsg := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, msg.Text)
				editMsg.ParseMode = "Markdown"
				if _, err := bot.Send(editMsg); err != nil {
					log.Printf("Error editing message: %v, sending new message instead", err)
					if _, err := bot.Send(msg); err != nil {
						log.Printf("Error sending message to %s (ID: %d): %v", chatTitle, chatID, err)
					}
				}
				continue
			}
		case "💵 USD/IRT":
			// Refresh USD to IRT price in the background
			go priceCache.updateUsdToIrrPrice()

			// Send a temporary message while refreshing
			tempMsg := tgbotapi.NewMessage(chatID, "⏳ *Fetching latest USD to IRT exchange rate...*")
			tempMsg.ParseMode = "Markdown"
			sentMsg, err := bot.Send(tempMsg)

			// Wait a bit for data to refresh
			time.Sleep(1 * time.Second)

			// Get the updated USD to IRT price
			usdToIrrPrice, priceErr := getUsdToIrrPrice()
			if priceErr != nil {
				msg.Text = fmt.Sprintf("Error getting USD to IRT exchange rate: %v", priceErr)
			} else {
				msg.Text = fmt.Sprintf("🔸 *USD to IRT*: %s Tomans", usdToIrrPrice)
			}

			// If we successfully sent the temp message, edit it
			if err == nil {
				editMsg := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, msg.Text)
				editMsg.ParseMode = "Markdown"
				if _, err := bot.Send(editMsg); err != nil {
					log.Printf("Error editing message: %v, sending new message instead", err)
					if _, err := bot.Send(msg); err != nil {
						log.Printf("Error sending message to %s (ID: %d): %v", chatTitle, chatID, err)
					}
				}
				continue
			}
		case "🏅 Gold/IRT":
			// Refresh Gold IRT price in the background
			go priceCache.updateGoldIrrPrice()

			// Send a temporary message while refreshing
			tempMsg := tgbotapi.NewMessage(chatID, "⏳ *Fetching latest Gold price in IRT...*")
			tempMsg.ParseMode = "Markdown"
			sentMsg, err := bot.Send(tempMsg)

			// Wait a bit for data to refresh
			time.Sleep(1 * time.Second)

			// Get the updated Gold IRT price
			goldIrrPrice, priceErr := getGoldPriceInIRR()
			if priceErr != nil {
				msg.Text = fmt.Sprintf("Error getting Gold price in IRT: %v", priceErr)
			} else {
				msg.Text = fmt.Sprintf("🔸 *Gold in IRT*: %s Tomans", goldIrrPrice)
			}

			// If we successfully sent the temp message, edit it
			if err == nil {
				editMsg := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, msg.Text)
				editMsg.ParseMode = "Markdown"
				if _, err := bot.Send(editMsg); err != nil {
					log.Printf("Error editing message: %v, sending new message instead", err)
					if _, err := bot.Send(msg); err != nil {
						log.Printf("Error sending message to %s (ID: %d): %v", chatTitle, chatID, err)
					}
				}
				continue
			}
		case "🔄 Refresh":
			go priceCache.refreshCache()
			msg.Text = "🔄 Refreshing price data. This may take a few seconds. Use /price to check the updated data."
		case "ℹ️ Status":
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
				msg.Text += fmt.Sprintf("\n\n📊 Price cache last updated: %s GMT", lastUpdate.Format("2006-01-02 15:04:05"))
			} else {
				msg.Text += "\n\n📊 Price cache has not been updated yet."
			}
		}

		// Send the response message
		if _, err := bot.Send(msg); err != nil {
			log.Printf("Error sending message to %s (ID: %d): %v", chatTitle, chatID, err)
		}
	}
}
