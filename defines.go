package main

import (
	"database/sql"
	"strconv"
	"time"

	"github.com/mmcdole/gofeed"
)

var (
	fetchInterval, _                  = time.ParseDuration(getEnv("FETCH_INTERVAL", "15m"))
	metadataInterval, _               = time.ParseDuration(getEnv("METADATA_INTERVAL", "12h"))
	historyInterval, _                = time.ParseDuration(getEnv("HISTORY_INTERVAL", "1h"))
	logLevel                          = getEnv("LOG_LEVEL", "INFO")
	webserverPort                     = getEnv("WEBSERVER_PORT", "8061")
	nip05Domain                       = getEnv("NIP05_DOMAIN", "atomstr.data.haus")
	maxWorkers, _                     = strconv.Atoi(getEnv("MAX_WORKERS", "5"))
	relaysToPublishTo                 = splitAndTrim(getEnv("RELAYS_TO_PUBLISH_TO", "wss://nostr.data.haus"))
	discoveryRelays                   = splitAndTrim(getEnv("ATOMSTR_DISCOVERY_RELAYS", "wss://nostr.data.haus,wss://relay.damus.io,wss://nos.lol,wss://relay.primal.net,wss://purplepag.es,wss://user.kindpag.es,wss://profiles.nostr1.com,wss://directory.yabu.me"))
	blasterRelays                     = splitAndTrim(getEnv("ATOMSTR_BLASTER_RELAYS", "wss://sendit.nosflare.com"))
	defaultFeedImage                  = getEnv("DEFAULT_FEED_IMAGE", "https://upload.wikimedia.org/wikipedia/en/thumb/4/43/Feed-icon.svg/256px-Feed-icon.svg.png")
	dbPath                            = getEnv("DB_PATH", "./atomstr.db")
	maxFailureAttempts, _             = strconv.Atoi(getEnv("MAX_FAILURE_ATTEMPTS", "3"))
	maxFailureDelete, _               = strconv.Atoi(getEnv("MAX_FAILURE_DELETE", "100"))
	brokenFeedRetryInterval, _        = time.ParseDuration(getEnv("BROKEN_FEED_RETRY_INTERVAL", "24h"))
	dryRunMode                        = false
	atomstrVersion             string = "0.9.13"
)

type Atomstr struct {
	db *sql.DB
}

var sqlInit = `
CREATE TABLE IF NOT EXISTS feeds (
	pub VARCHAR(64) PRIMARY KEY,
	sec VARCHAR(64) NOT NULL,
	url TEXT NOT NULL,
	state TEXT DEFAULT 'active',
	failure_count INTEGER DEFAULT 0,
	last_success DATETIME,
	last_failure DATETIME
);
`

type feedStruct struct {
	URL          string
	Sec          string
	Pub          string
	Npub         string
	Title        string
	Description  string
	Link         string
	Image        string
	Posts        []*gofeed.Item
	State        string
	FailureCount int
	LastSuccess  *time.Time
	LastFailure  *time.Time
	ETag         string
	LastModified string
}

type webIndex struct {
	Relays  []string
	Feeds   []feedStruct
	Version string
}
type webAddFeed struct {
	Status string
	Feed   feedStruct
}

type asyncJob struct {
	ID      string
	URL     string
	Status  string // "processing", "completed", "failed"
	Message string
	Error   string
	FeedURL string
	Npub    string
}

type asyncResponse struct {
	JobID string `json:"job_id"`
	Error string `json:"error,omitempty"`
}

type statusResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	URL     string `json:"url,omitempty"`
	Npub    string `json:"npub,omitempty"`
}
