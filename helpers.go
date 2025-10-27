package main

import (
	"database/sql"
	"log"
	"os"
	"time"

	"github.com/hashicorp/logutils"
	"github.com/nbd-wtf/go-nostr"
)

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

func checkMaxAge(itemTime *time.Time, maxAgeHours time.Duration) bool {
	maxAge := time.Now().UTC().Add(-maxAgeHours) // make sure everything is UTC!

	if itemTime.UTC().After(maxAge) {
		return true
	}
	return false
}

func dbInit() *sql.DB {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("[FATAL] open db: %v", err)
	}
	log.Printf("[INFO] database opened at %s", dbPath)
	// defer db.Close()

	_, err = db.Exec(sqlInit)
	if err != nil {
		log.Printf("%q: %s\n", err, sqlInit)
	}

	return db
}

func generateKeysForUrl(feedUrl string) *feedStruct {
	feedElem := feedStruct{}
	feedElem.Url = feedUrl

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
