package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/logutils"
	"github.com/mmcdole/gofeed"
	"github.com/nbd-wtf/go-nostr"
)

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

func checkMaxAge(itemTime *time.Time, maxAgeHours time.Duration) bool {
	maxAge := time.Now().UTC().Add(-maxAgeHours) // make sure everything is UTC!

	return itemTime.UTC().After(maxAge)
}

func dbInit() *sql.DB {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("[FATAL] open db: %v", err)
	}
	log.Printf("[DEBUG] database opened at %s", dbPath)
	// defer db.Close()

	_, err = db.Exec(sqlInit)
	if err != nil {
		log.Printf("[ERROR] %q: %s\n", err, sqlInit)
	}

	// Migrate existing databases to add new columns
	migrateDB(db)

	return db
}

func migrateDB(db *sql.DB) {
	// Check if state column exists
	var stateExists bool
	err := db.QueryRow(`
		SELECT COUNT(*) > 0 
		FROM pragma_table_info('feeds') 
		WHERE name = 'state'
	`).Scan(&stateExists)
	if err != nil {
		log.Printf("[WARN] Failed to check for state column: %v", err)
		return
	}

	if !stateExists {
		log.Println("[INFO] Migrating database: adding state tracking columns")
		_, err := db.Exec(`
			ALTER TABLE feeds ADD COLUMN state TEXT DEFAULT 'active';
			ALTER TABLE feeds ADD COLUMN failure_count INTEGER DEFAULT 0;
			ALTER TABLE feeds ADD COLUMN last_success DATETIME;
			ALTER TABLE feeds ADD COLUMN last_failure DATETIME;
		`)
		if err != nil {
			log.Printf("[ERROR] Failed to migrate database: %v", err)
		} else {
			log.Println("[INFO] Database migration completed successfully")
		}
	}

	// Check if etag column exists
	var etagExists bool
	err = db.QueryRow(`
		SELECT COUNT(*) > 0 
		FROM pragma_table_info('feeds') 
		WHERE name = 'etag'
	`).Scan(&etagExists)
	if err != nil {
		log.Printf("[WARN] Failed to check for etag column: %v", err)
		return
	}

	if !etagExists {
		log.Println("[INFO] Migrating database: adding HTTP caching columns")
		_, err := db.Exec(`
			ALTER TABLE feeds ADD COLUMN etag TEXT DEFAULT '';
			ALTER TABLE feeds ADD COLUMN last_modified TEXT DEFAULT '';
		`)
		if err != nil {
			log.Printf("[ERROR] Failed to migrate database for caching columns: %v", err)
		} else {
			log.Println("[INFO] HTTP caching columns migration completed")
		}
	}
}

// feedURLToNip05Name converts a feed URL into a NIP-05-compliant local-part.
// NIP-05 local-parts must only contain a-z0-9-_.
func feedURLToNip05Name(feedURL string) string {
	parsed, err := url.Parse(feedURL)
	if err != nil {
		// fallback: strip everything non-compliant
		re := regexp.MustCompile(`[^a-z0-9._-]`)
		return re.ReplaceAllString(strings.ToLower(feedURL), "_")
	}

	host := strings.ToLower(parsed.Host)
	host = strings.TrimPrefix(host, "www.")

	path := strings.ToLower(parsed.Path)
	path = strings.ReplaceAll(path, "/", "_")

	slug := host + path

	// strip leading/trailing underscores
	slug = strings.Trim(slug, "_")

	// remove any character not in a-z0-9._-
	re := regexp.MustCompile(`[^a-z0-9._-]`)
	slug = re.ReplaceAllString(slug, "")

	return slug
}

func generateKeysForURL(feedURL string) *feedStruct {
	feedElem := feedStruct{}
	feedElem.URL = feedURL

	feedElem.Sec = nostr.GeneratePrivateKey() // generate new key
	feedElem.Pub, _ = nostr.GetPublicKey(feedElem.Sec)

	return &feedElem
}

func logger() {
	filter := &logutils.LevelFilter{
		Levels:   []logutils.LogLevel{"DEBUG", "INFO", "WARN", "ERROR", "FATAL"},
		MinLevel: logutils.LogLevel(logLevel),
		Writer:   os.Stderr,
	}
	log.SetOutput(filter)
}

// getDateFormats returns the list of date formats to try when parsing feed dates
// Can be customized via ATOMSTR_DATE_FORMATS environment variable (comma-separated)
func getDateFormats() []string {
	// Default date formats for RSS/Atom feeds
	defaultFormats := []string{
		time.RFC1123Z,                    // "Mon, 02 Jan 2006 15:04:05 -0700"
		time.RFC1123,                     // "Mon, 02 Jan 2006 15:04:05 MST"
		time.RFC822,                      // "02 Jan 06 15:04 MST"
		time.RFC822Z,                     // "02 Jan 06 15:04 -0700"
		time.RFC3339,                     // "2006-01-02T15:04:05Z07:00"
		time.RFC3339Nano,                 // "2006-01-02T15:04:05.999999999Z07:00"
		"2006-01-02T15:04:05Z",           // ISO8601 UTC
		"2006-01-02T15:04:05-07:00",      // ISO8601 with timezone
		"2006-01-02 15:04:05",            // Simple datetime
		"2006-01-02",                     // Date only
		"Mon, 2 Jan 2006 15:04:05 -0700", // RSS without leading zero
		"Mon, 2 Jan 2006 15:04:05 MST",   // RSS without leading zero
		"2 January 2006 - 15:04",         // NL Times style (day month year - time)
		"2006-01-02 T15:04:05Z07:00",     // ISO8601 with space before T
	}

	// Check for custom date formats from environment
	if customFormats := os.Getenv("ATOMSTR_DATE_FORMATS"); customFormats != "" {
		formats := strings.Split(customFormats, ",")
		for i, format := range formats {
			formats[i] = strings.TrimSpace(format)
		}
		log.Printf("[INFO] Using %d custom date formats from environment", len(formats))
		return formats
	}

	return defaultFormats
}

// parseFeedDate attempts to parse date from feed item with fallbacks
// Order: PublishedParsed -> UpdatedParsed -> Published string -> Updated string
func parseFeedDate(feedPost *gofeed.Item) (*time.Time, error) {
	// Primary: PublishedParsed
	if feedPost.PublishedParsed != nil {
		// log.Printf("[DEBUG] Using PublishedParsed date for %s", feedPost.Title)
		return feedPost.PublishedParsed, nil
	}

	// Fallback 1: UpdatedParsed
	if feedPost.UpdatedParsed != nil {
		log.Printf("[DEBUG] Using UpdatedParsed date for %s", feedPost.Title)
		return feedPost.UpdatedParsed, nil
	}

	dateFormats := getDateFormats()

	// Fallback 2: Published string
	if feedPost.Published != "" {
		for _, format := range dateFormats {
			if parsedTime, err := time.Parse(format, feedPost.Published); err == nil {
				log.Printf("[DEBUG] Parsed Published string '%s' using format '%s' for %s", feedPost.Published, format, feedPost.Title)
				return &parsedTime, nil
			}
		}
		log.Printf("[WARN] Failed to parse Published string '%s' for %s", feedPost.Published, feedPost.Title)
	}

	// Fallback 3: Updated string
	if feedPost.Updated != "" {
		for _, format := range dateFormats {
			if parsedTime, err := time.Parse(format, feedPost.Updated); err == nil {
				log.Printf("[DEBUG] Parsed Updated string '%s' using format '%s' for %s", feedPost.Updated, format, feedPost.Title)
				return &parsedTime, nil
			}
		}
		log.Printf("[WARN] Failed to parse Updated string '%s' for %s", feedPost.Updated, feedPost.Title)
	}

	return nil, fmt.Errorf("no valid date found in feed item")
}
