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
	"sync/atomic"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/mmcdole/gofeed"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

var (
	sanitizerPolicy = func() *bluemonday.Policy {
		p := bluemonday.StrictPolicy()
		p.AllowImages()
		p.AllowStandardURLs()
		p.AllowAttrs("href").OnElements("a")
		return p
	}()
	reNitterTelegram = regexp.MustCompile(`nitter|telegram`)
	reInlineImg      = regexp.MustCompile(`<img.src="(http.*\.(jpg|png|gif)).*/>`)
	reInlineLink     = regexp.MustCompile(`<a.href="(https.*?)" .*</a>`)
)

var publishedPosts = struct {
	sync.RWMutex
	items map[string]time.Time
}{items: make(map[string]time.Time)}

func isPostPublished(feedURL, postID string) bool {
	key := feedURL + "|" + postID
	publishedPosts.RLock()
	_, exists := publishedPosts.items[key]
	publishedPosts.RUnlock()
	return exists
}

func markPostPublished(feedURL, postID string) {
	key := feedURL + "|" + postID
	publishedPosts.Lock()
	publishedPosts.items[key] = time.Now()
	publishedPosts.Unlock()
}

func prunePublishedPosts(maxAge time.Duration) {
	publishedPosts.Lock()
	defer publishedPosts.Unlock()
	cutoff := time.Now().Add(-maxAge)
	for k, t := range publishedPosts.items {
		if t.Before(cutoff) {
			delete(publishedPosts.items, k)
		}
	}
}

func (a *Atomstr) dbGetAllFeeds() (*[]feedStruct, error) {
	sqlStatement := `SELECT pub, sec, url, state, failure_count, last_success, last_failure, etag, last_modified FROM feeds`
	rows, err := a.db.Query(sqlStatement)
	if err != nil {
		return nil, fmt.Errorf("returning feeds from DB failed: %w", err)
	}

	feedItems := []feedStruct{}

	for rows.Next() {
		feedItem := feedStruct{}
		if err := rows.Scan(&feedItem.Pub, &feedItem.Sec, &feedItem.URL, &feedItem.State, &feedItem.FailureCount, &feedItem.LastSuccess, &feedItem.LastFailure, &feedItem.ETag, &feedItem.LastModified); err != nil {
			return nil, fmt.Errorf("scanning for feeds failed: %w", err)
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

// fetchFeedWithCaching fetches a feed URL using HTTP conditional GET.
// Returns the parsed feed, new ETag, new Last-Modified, whether the feed was not modified, and any error.
func fetchFeedWithCaching(feedURL string, etag string, lastModified string) (*gofeed.Feed, string, string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", feedURL, nil)
	if err != nil {
		return nil, "", "", false, err
	}
	req.Header.Set("User-Agent", "atomstr/"+atomstrVersion)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		log.Printf("[DEBUG] Feed %s not modified (304)", feedURL)
		return nil, etag, lastModified, true, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", "", false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	fp := gofeed.NewParser()
	fp.UserAgent = "atomstr/" + atomstrVersion
	feed, err := fp.Parse(resp.Body)
	if err != nil {
		return nil, "", "", false, err
	}

	newETag := resp.Header.Get("ETag")
	newLastMod := resp.Header.Get("Last-Modified")
	return feed, newETag, newLastMod, false, nil
}

func (a *Atomstr) processFeedURL(ch chan feedStruct, wg *sync.WaitGroup, stats *scrapeStats) {
	for feedItem := range ch {

		// Check if we should fetch this feed
		if !a.shouldFetchFeed(feedItem) {
			log.Printf("[DEBUG] Skipping broken feed %s (last failure: %v)", feedItem.URL, feedItem.LastFailure)
			atomic.AddInt64(&stats.feedsSkipped, 1)
			continue
		}

		feed, newETag, newLastMod, notModified, err := fetchFeedWithCaching(feedItem.URL, feedItem.ETag, feedItem.LastModified)

		if notModified {
			a.dbResetFeedState(feedItem.URL)
			atomic.AddInt64(&stats.feedsCached, 1)
			atomic.AddInt64(&stats.feedsProcessed, 1)
			continue
		}

		if err != nil {
			log.Println("[ERROR] Can't update feed", feedItem.URL)
			atomic.AddInt64(&stats.feedsErrored, 1)

			// Update failure state
			newFailureCount := feedItem.FailureCount + 1

			// Auto-delete feeds that exceed the maximum failure threshold
			if newFailureCount >= maxFailureDelete {
				log.Printf("[WARN] Feed %s auto-deleted after %d consecutive failures", feedItem.URL, newFailureCount)
				a.deleteSource(feedItem.URL)
				continue
			}

			newState := "active"
			if newFailureCount >= maxFailureAttempts {
				newState = "broken"
				log.Printf("[WARN] Feed %s marked as broken after %d failures", feedItem.URL, newFailureCount)
			}
			now := time.Now()
			a.dbUpdateFeedState(feedItem.URL, newState, newFailureCount, feedItem.LastSuccess, &now)
		} else {
			log.Println("[DEBUG] Updating feed ", feedItem.URL)
			atomic.AddInt64(&stats.feedsProcessed, 1)

			// Reset state on successful fetch
			a.dbResetFeedState(feedItem.URL)
			a.dbUpdateFeedCache(feedItem.URL, newETag, newLastMod)

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
				processFeedPost(feedItem, feed.Items[i], fetchInterval, stats)
			}
			log.Println("[DEBUG] Finished updating feed ", feedItem.URL)
		}

	}
	wg.Done()
}

func processFeedPost(feedItem feedStruct, feedPost *gofeed.Item, interval time.Duration, stats *scrapeStats) {
	// Parse date with fallbacks
	itemTime, err := parseFeedDate(feedPost)
	if err != nil {
		log.Printf("[WARN] Can't parse any date from post from %s: %v", feedItem.URL, err)
		return
	}

	// if time right, then push
	if checkMaxAge(itemTime, interval) {
		// Dedup: use GUID if available, fall back to Link
		postID := feedPost.GUID
		if postID == "" {
			postID = feedPost.Link
		}
		if postID != "" && isPostPublished(feedItem.URL, postID) {
			log.Printf("[DEBUG] Skipping duplicate post %s from %s", postID, feedItem.URL)
			return
		}

		var feedText string
		if reNitterTelegram.MatchString(feedPost.Link) { // fix duplicated title in nitter/telegram
			feedText = sanitizerPolicy.Sanitize(feedPost.Description)
		} else {
			feedText = feedPost.Title + "\n\n" + sanitizerPolicy.Sanitize(feedPost.Description)
		}
		// fmt.Println(feedText)

		feedText = reInlineImg.ReplaceAllString(feedText, "$1\n") // allow inline images

		feedText = reInlineLink.ReplaceAllString(feedText, "$1\n") // allow inline links

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
		if stats != nil {
			atomic.AddInt64(&stats.postsPublished, 1)
		}

		if postID != "" {
			markPostPublished(feedItem.URL, postID)
		}
	}
}

func (a *Atomstr) dbWriteFeed(feedItem *feedStruct) error {
	_, err := a.db.Exec(`insert into feeds (pub, sec, url, state, failure_count, last_success, last_failure) values(?, ?, ?, ?, ?, ?, ?)`,
		feedItem.Pub, feedItem.Sec, feedItem.URL, feedItem.State, feedItem.FailureCount, feedItem.LastSuccess, feedItem.LastFailure)
	if err != nil {
		return fmt.Errorf("can't add feed: %w", err)
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
		log.Println("[DEBUG] Feed not found in DB")
	}
	return &feedItem
}

func checkValidFeedSource(feedURL string) (*feedStruct, error) {
	log.Printf("[DEBUG] checkValidFeedSource called for: %s", feedURL)
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
		processFeedPost(*feedItem, feedItem.Posts[i], historyInterval, nil)
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
			return fmt.Errorf("can't remove feed: %w", err)
		}
		log.Println("[INFO] feed removed")
		return nil
	} else {
		return fmt.Errorf("feed not found")
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
		return fmt.Errorf("can't update feed state: %w", err)
	}
	return nil
}

func (a *Atomstr) dbResetFeedState(feedURL string) error {
	now := time.Now()
	return a.dbUpdateFeedState(feedURL, "active", 0, &now, nil)
}

func (a *Atomstr) dbUpdateFeedCache(feedURL string, etag string, lastModified string) error {
	_, err := a.db.Exec(`UPDATE feeds SET etag = ?, last_modified = ? WHERE url = ?`,
		etag, lastModified, feedURL)
	if err != nil {
		return fmt.Errorf("can't update feed cache headers: %w", err)
	}
	return nil
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
