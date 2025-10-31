package main

import (
	"context"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/mmcdole/gofeed"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

func (a *Atomstr) dbGetAllFeeds() (*[]feedStruct, error) {
	sqlStatement := `SELECT pub, sec, url, state, failure_count, last_success, last_failure FROM feeds`
	rows, err := a.db.Query(sqlStatement)
	if err != nil {
		return nil, fmt.Errorf("[ERROR] Returning feeds from DB failed: %w", err)
	}

	feedItems := []feedStruct{}

	for rows.Next() {
		feedItem := feedStruct{}
		if err := rows.Scan(&feedItem.Pub, &feedItem.Sec, &feedItem.URL, &feedItem.State, &feedItem.FailureCount, &feedItem.LastSuccess, &feedItem.LastFailure); err != nil {
			return nil, fmt.Errorf("[ERROR] Scanning for feeds failed: %w", err)
		}
		feedItem.Npub, _ = nip19.EncodePublicKey(feedItem.Pub)
		feedItems = append(feedItems, feedItem)
	}

	return &feedItems, nil
}

// func processFeedURL(ch chan string, wg *sync.WaitGroup, feedItem *feedStruct) {
func fetchFavicon(feedURL string) string {
	parsedURL, err := url.Parse(feedURL)
	if err != nil {
		return defaultFeedImage
	}

	baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

	// Try common favicon locations
	// Prioritize larger, modern formats first
	faviconURLs := []string{
		baseURL + "/apple-touch-icon.png",
		baseURL + "/apple-touch-icon-precomposed.png",
		baseURL + "/icon.svg",
		baseURL + "/favicon.png",
		baseURL + "/favicon.ico",
	}

	client := &http.Client{Timeout: 5 * time.Second}

	for _, faviconURL := range faviconURLs {
		resp, err := client.Head(faviconURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return faviconURL
		}
		if resp != nil {
			resp.Body.Close()
		}
	}

	// Try to parse HTML to find favicon link
	resp, err := client.Get(baseURL)
	if err != nil {
		return defaultFeedImage
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return defaultFeedImage
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		return defaultFeedImage
	}

	// Simple favicon link extraction (basic implementation)
	// Extract favicon URLs from HTML using regex
	body, _ := io.ReadAll(resp.Body)
	htmlContent := string(body)

	// Look for various favicon link tags
	patterns := []string{
		`<link[^>]+rel=["'](?:apple-touch-icon|icon|shortcut icon)["'][^>]+href=["']([^"']+)["']`,
		`<link[^>]+href=["']([^"']+)["'][^>]+rel=["'](?:apple-touch-icon|icon|shortcut icon)["']`,
	}

	var bestIcon string

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(htmlContent, -1)
		for _, match := range matches {
			if len(match) > 1 {
				iconURL := match[1]
				if strings.HasPrefix(iconURL, "/") {
					iconURL = baseURL + iconURL
				}
				// Simple size detection - prefer larger icons
				if strings.Contains(iconURL, "192") || strings.Contains(iconURL, "180") {
					return iconURL
				}
				bestIcon = iconURL
			}
		}
	}

	if bestIcon != "" {
		return bestIcon
	}

	return defaultFeedImage
}

func processFeedURL(ch chan feedStruct, wg *sync.WaitGroup) {
	for feedItem := range ch {
		// Get the Atomstr instance to check state and update database
		a := &Atomstr{db: dbInit()}

		// Check if we should fetch this feed
		if !a.shouldFetchFeed(feedItem) {
			log.Printf("[INFO] Skipping broken feed %s (last failure: %v)", feedItem.URL, feedItem.LastFailure)
			a.db.Close()
			// wg.Done() # not anymore? fix negative wg counter
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // fetch feeds with 10s timeout
		fp := gofeed.NewParser()
		fp.UserAgent = "atomstr/" + atomstrVersion
		feed, err := fp.ParseURLWithContext(feedItem.URL, ctx)
		cancel() // Cancel immediately after use

		if err != nil {
			log.Println("[ERROR] Can't update feed", feedItem.URL)

			// Update failure state
			newFailureCount := feedItem.FailureCount + 1
			newState := "active"
			if newFailureCount >= maxFailureAttempts {
				newState = "broken"
				log.Printf("[WARN] Feed %s marked as broken after %d failures", feedItem.URL, newFailureCount)
			}
			now := time.Now()
			a.dbUpdateFeedState(feedItem.URL, newState, newFailureCount, feedItem.LastSuccess, &now)
		} else {
			log.Println("[DEBUG] Updating feed ", feedItem.URL)

			// Reset state on successful fetch
			a.dbResetFeedState(feedItem.URL)

			// fmt.Println(feed)
			feedItem.Title = feed.Title
			feedItem.Description = feed.Description
			feedItem.Link = feed.Link
			if feed.Image != nil {
				feedItem.Image = feed.Image.URL
			} else {
				feedItem.Image = fetchFavicon(feedItem.URL)
				if feedItem.Image == defaultFeedImage {
					log.Println("[DEBUG] No favicon found for", feedItem.URL, "using default image")
				} else {
					log.Println("[DEBUG] Using favicon for", feedItem.URL, ":", feedItem.Image)
				}
			}
			// feedItem.Image = feed.Image

			for i := range feed.Items {
				processFeedPost(feedItem, feed.Items[i], fetchInterval)
			}
			log.Println("[DEBUG] Finished updating feed ", feedItem.URL)
		}

		a.db.Close()
	}
	wg.Done()
}

func processFeedPost(feedItem feedStruct, feedPost *gofeed.Item, interval time.Duration) {
	p := bluemonday.StrictPolicy() // initialize html sanitizer
	p.AllowImages()
	p.AllowStandardURLs()
	p.AllowAttrs("href").OnElements("a")

	// Debug PublishedParsed
	log.Printf("[DEBUG] Feed: %s, Title: %s", feedItem.URL, feedPost.Title)
	log.Printf("[DEBUG] PublishedParsed: %v", feedPost.PublishedParsed)
	log.Printf("[DEBUG] Published: %s", feedPost.Published)
	log.Printf("[DEBUG] UpdatedParsed: %v", feedPost.UpdatedParsed)
	log.Printf("[DEBUG] Updated: %s", feedPost.Updated)

	// Parse date with fallbacks
	itemTime, err := parseFeedDate(feedPost)
	if err != nil {
		log.Printf("[WARN] Can't parse any date from post from %s: %v", feedItem.URL, err)
		return
	}

	log.Printf("[DEBUG] Parsed itemTime: %v", itemTime)
	log.Printf("[DEBUG] Current time: %v", time.Now().UTC())
	log.Printf("[DEBUG] Interval: %v", interval)
	log.Printf("[DEBUG] checkMaxAge result: %v", checkMaxAge(itemTime, interval))

	// if time right, then push
	if checkMaxAge(itemTime, interval) {
		var feedText string
		re := regexp.MustCompile(`nitter|telegram`)
		if re.MatchString(feedPost.Link) { // fix duplicated title in nitter/telegram
			feedText = p.Sanitize(feedPost.Description)
		} else {
			feedText = feedPost.Title + "\n\n" + p.Sanitize(feedPost.Description)
		}
		// fmt.Println(feedText)

		regImg := regexp.MustCompile(`\<img.src=\"(http.*\.(jpg|png|gif)).*\/\>`) // allow inline images
		feedText = regImg.ReplaceAllString(feedText, "$1\n")

		regLink := regexp.MustCompile(`\<a.href=\"(https.*?)\"\ .*\<\/a\>`) // allow inline links
		feedText = regLink.ReplaceAllString(feedText, "$1\n")

		feedText = html.UnescapeString(feedText) // decode html strings

		if feedPost.Enclosures != nil { // allow enclosure images/links
			for _, enclosure := range feedPost.Enclosures {
				feedText = feedText + "\n\n" + enclosure.URL
			}
		}

		if feedPost.Link != "" {
			feedText = feedText + "\n\n" + feedPost.Link
		}

		var tags nostr.Tags

		if feedPost.Categories != nil { // use post categories as tags
			for _, category := range feedPost.Categories {
				tags = append(tags, nostr.Tag{"t", category})
			}
		}

		tags = append(tags, nostr.Tag{"proxy", feedItem.URL + `#` + url.QueryEscape(feedPost.Link), "rss"})

		ev := nostr.Event{
			PubKey:    feedItem.Pub,
			CreatedAt: nostr.Timestamp(feedPost.PublishedParsed.Unix()),
			Kind:      nostr.KindTextNote,
			Tags:      tags,
			Content:   feedText,
		}

		ev.Sign(feedItem.Sec)

		nostrPostItem(ev)
	}
}

func (a *Atomstr) dbWriteFeed(feedItem *feedStruct) error {
	_, err := a.db.Exec(`insert into feeds (pub, sec, url, state, failure_count, last_success, last_failure) values(?, ?, ?, ?, ?, ?, ?)`,
		feedItem.Pub, feedItem.Sec, feedItem.URL, feedItem.State, feedItem.FailureCount, feedItem.LastSuccess, feedItem.LastFailure)
	if err != nil {
		return fmt.Errorf("[ERROR] Can't add feed: %w", err)
	}
	nip19Pub, _ := nip19.EncodePublicKey(feedItem.Pub)
	log.Println("[INFO] Added feed " + feedItem.URL + " with public key " + nip19Pub)
	return nil
}

func (a *Atomstr) dbGetFeed(feedURL string) *feedStruct {
	sqlStatement := `SELECT pub, sec, url, state, failure_count, last_success, last_failure FROM feeds WHERE url=?;`
	row := a.db.QueryRow(sqlStatement, feedURL)

	feedItem := feedStruct{}
	err := row.Scan(&feedItem.Pub, &feedItem.Sec, &feedItem.URL, &feedItem.State, &feedItem.FailureCount, &feedItem.LastSuccess, &feedItem.LastFailure)
	if err != nil {
		log.Println("[INFO] Feed not found in DB")
	}
	return &feedItem
}

func checkValidFeedSource(feedURL string) (*feedStruct, error) {
	log.Printf("[INFO] checkValidFeedSource called for: %s", feedURL)
	log.Println("[DEBUG] Trying to find feed at", feedURL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fp := gofeed.NewParser()
	fp.UserAgent = "atomstr/" + atomstrVersion
	feed, err := fp.ParseURLWithContext(feedURL, ctx)
	feedItem := feedStruct{}

	if err != nil {
		log.Println("[ERROR] Not a valid feed source")
		return &feedItem, err
	}
	// FIXME! That needs proper error handling.
	feedItem.URL = feedURL
	feedItem.Title = feed.Title
	feedItem.Description = feed.Description
	feedItem.Link = feed.Link
	if feed.Image != nil {
		feedItem.Image = feed.Image.URL
	} else {
		feedItem.Image = fetchFavicon(feedURL)
		if feedItem.Image == defaultFeedImage {
			log.Println("[DEBUG] No favicon found for", feedURL, "using default image")
		} else {
			log.Println("[DEBUG] Using favicon for", feedURL, ":", feedItem.Image)
		}
	}
	feedItem.Posts = feed.Items

	// Initialize state fields
	feedItem.State = "active"
	feedItem.FailureCount = 0
	feedItem.LastSuccess = nil
	feedItem.LastFailure = nil

	return &feedItem, err
}

func (a *Atomstr) addSource(feedURL string) (*feedStruct, error) {
	// var feedElem2 *feedStruct
	feedItem, err := checkValidFeedSource(feedURL)
	// if feedItem.Title == "" {
	if err != nil {
		log.Println("[ERROR] No valid feed found on", feedURL)
		return feedItem, err
	}

	// check for existing feed
	feedTest := a.dbGetFeed(feedURL)
	if feedTest.URL != "" {
		log.Println("[WARN] Feed already exists")
		return feedItem, err
	}

	feedItemKeys := generateKeysForURL(feedURL)
	feedItem.Pub = feedItemKeys.Pub
	feedItem.Sec = feedItemKeys.Sec

	// Initialize state fields for new feeds
	feedItem.State = "active"
	feedItem.FailureCount = 0
	now := time.Now()
	feedItem.LastSuccess = &now
	feedItem.LastFailure = nil

	fmt.Println(feedItem.Pub)

	if err := a.dbWriteFeed(feedItem); err != nil {
		return feedItem, err
	}
	if !dryRunMode {
		nostrUpdateFeedMetadata(feedItem)
	}

	log.Println("[INFO] Parsing post history of new feed")
	for i := range feedItem.Posts {
		processFeedPost(*feedItem, feedItem.Posts[i], historyInterval)
	}
	log.Println("[INFO] Finished parsing post history of new feed")

	return feedItem, err
}

func (a *Atomstr) deleteSource(feedURL string) error {
	// check for existing feed
	feedTest := a.dbGetFeed(feedURL)
	if feedTest.URL != "" {
		sqlStatement := `DELETE FROM feeds WHERE url=?;`
		_, err := a.db.Exec(sqlStatement, feedURL)
		if err != nil {
			return fmt.Errorf("[WARN] Can't remove feed: %w", err)
		}
		log.Println("[INFO] feed removed")
		return nil
	} else {
		return fmt.Errorf("[WARN] feed not found")
	}
}

func (a *Atomstr) listFeeds() error {
	feeds, err := a.dbGetAllFeeds()
	if err != nil {
		return err
	}

	for _, feedItem := range *feeds {
		nip19Pub, _ := nip19.EncodePublicKey(feedItem.Pub)
		fmt.Print(nip19Pub + " ")
		fmt.Print(feedItem.URL)
		if feedItem.State != "active" {
			fmt.Printf(" [%s, failures: %d]", feedItem.State, feedItem.FailureCount)
		}
		fmt.Println()
	}
	return nil
}

func (a *Atomstr) dbUpdateFeedState(feedURL string, state string, failureCount int, lastSuccess *time.Time, lastFailure *time.Time) error {
	_, err := a.db.Exec(`UPDATE feeds SET state = ?, failure_count = ?, last_success = ?, last_failure = ? WHERE url = ?`,
		state, failureCount, lastSuccess, lastFailure, feedURL)
	if err != nil {
		return fmt.Errorf("[ERROR] Can't update feed state: %w", err)
	}
	return nil
}

func (a *Atomstr) dbResetFeedState(feedURL string) error {
	now := time.Now()
	return a.dbUpdateFeedState(feedURL, "active", 0, &now, nil)
}

func (a *Atomstr) shouldFetchFeed(feedItem feedStruct) bool {
	if feedItem.State != "broken" {
		return true
	}

	// For broken feeds, only try once a day
	if feedItem.LastFailure != nil {
		timeSinceFailure := time.Since(*feedItem.LastFailure)
		return timeSinceFailure >= brokenFeedRetryInterval
	}

	return true
}
