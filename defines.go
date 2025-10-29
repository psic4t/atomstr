package main

import (
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
)

var (
	fetchInterval, _           = time.ParseDuration(getEnv("FETCH_INTERVAL", "15m"))
	metadataInterval, _        = time.ParseDuration(getEnv("METADATA_INTERVAL", "12h"))
	historyInterval, _         = time.ParseDuration(getEnv("HISTORY_INTERVAL", "1h"))
	logLevel                   = getEnv("LOG_LEVEL", "DEBUG")
	webserverPort              = getEnv("WEBSERVER_PORT", "8061")
	nip05Domain                = getEnv("NIP05_DOMAIN", "atomstr.data.haus")
	maxWorkers, _              = strconv.Atoi(getEnv("MAX_WORKERS", "5"))
	r                          = getEnv("RELAYS_TO_PUBLISH_TO", "wss://nostr.data.haus")
	relaysToPublishTo          = strings.Split(r, ", ")
	defaultFeedImage           = getEnv("DEFAULT_FEED_IMAGE", "https://upload.wikimedia.org/wikipedia/en/thumb/4/43/Feed-icon.svg/256px-Feed-icon.svg.png")
	dbPath                     = getEnv("DB_PATH", "./atomstr.db")
	dryRunMode                 = false
	atomstrVersion      string = "0.9.8"
)

type Atomstr struct {
	db *sql.DB
}

var sqlInit = `
CREATE TABLE IF NOT EXISTS feeds (
	pub VARCHAR(64) PRIMARY KEY,
	sec VARCHAR(64) NOT NULL,
	url TEXT NOT NULL
);
`

type feedStruct struct {
	URL         string
	Sec         string
	Pub         string
	Npub        string
	Title       string
	Description string
	Link        string
	Image       string
	Posts       []*gofeed.Item
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
