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

func (a *Atomstr) dbGetAllFeeds() *[]feedStruct {
	sqlStatement := `SELECT pub, sec, url FROM feeds`
	rows, err := a.db.Query(sqlStatement)
	if err != nil {
		log.Fatal("[ERROR] Returning feeds from DB failed")
	}

	feedItems := []feedStruct{}

	for rows.Next() {
		feedItem := feedStruct{}
		if err := rows.Scan(&feedItem.Pub, &feedItem.Sec, &feedItem.Url); err != nil {
			log.Fatal("[ERROR] Scanning for feeds failed")
		}
		feedItem.Npub, _ = nip19.EncodePublicKey(feedItem.Pub)
		feedItems = append(feedItems, feedItem)
	}

	return &feedItems
}

// func processFeedUrl(ch chan string, wg *sync.WaitGroup, feedItem *feedStruct) {
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

func processFeedUrl(ch chan feedStruct, wg *sync.WaitGroup) {
	for feedItem := range ch {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // fetch feeds with 10s timeout
		defer cancel()
		fp := gofeed.NewParser()
		feed, err := fp.ParseURLWithContext(feedItem.Url, ctx)
		if err != nil {
			log.Println("[ERROR] Can't update feed", feedItem.Url)
		} else {
			log.Println("[DEBUG] Updating feed ", feedItem.Url)
			// fmt.Println(feed)
			feedItem.Title = feed.Title
			feedItem.Description = feed.Description
			feedItem.Link = feed.Link
			if feed.Image != nil {
				feedItem.Image = feed.Image.URL
			} else {
				feedItem.Image = fetchFavicon(feedItem.Url)
				if feedItem.Image == defaultFeedImage {
					log.Println("[DEBUG] No favicon found for", feedItem.Url, "using default image")
				} else {
					log.Println("[DEBUG] Using favicon for", feedItem.Url, ":", feedItem.Image)
				}
			}
			// feedItem.Image = feed.Image

			for i := range feed.Items {
				processFeedPost(feedItem, feed.Items[i], fetchInterval)
			}
			log.Println("[DEBUG] Finished updating feed ", feedItem.Url)
		}
	}
	wg.Done()
}

func processFeedPost(feedItem feedStruct, feedPost *gofeed.Item, interval time.Duration) {
	p := bluemonday.StrictPolicy() // initialize html sanitizer
	p.AllowImages()
	p.AllowStandardURLs()
	p.AllowAttrs("href").OnElements("a")

	// fmt.Println(feedPost.PublishedParsed)

	// ditch it, if no timestamp
	if feedPost.PublishedParsed == nil {
		log.Println("[WARN] Can't read PublishedParsed date of post from", feedItem.Url)
		return
	}
	// if time right, then push
	if checkMaxAge(feedPost.PublishedParsed, interval) {
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

		tags = append(tags, nostr.Tag{"proxy", feedItem.Url + `#` + url.QueryEscape(feedPost.Link), "rss"})

		ev := nostr.Event{
			PubKey:    feedItem.Pub,
			CreatedAt: nostr.Timestamp(feedPost.PublishedParsed.Unix()),
			Kind:      nostr.KindTextNote,
			Tags:      tags,
			Content:   feedText,
		}

		ev.Sign(feedItem.Sec)

		if noPub == false {
			nostrPostItem(ev)
		}
	}
}

func (a *Atomstr) dbWriteFeed(feedItem *feedStruct) bool {
	_, err := a.db.Exec(`insert into feeds (pub, sec, url) values(?, ?, ?)`, feedItem.Pub, feedItem.Sec, feedItem.Url)
	if err != nil {
		log.Println("[ERROR] Can't add feed!")
		log.Fatal(err)
	}
	nip19Pub, _ := nip19.EncodePublicKey(feedItem.Pub)
	log.Println("[INFO] Added feed " + feedItem.Url + " with public key " + nip19Pub)
	return true
}

func (a *Atomstr) dbGetFeed(feedUrl string) *feedStruct {
	sqlStatement := `SELECT pub, sec, url FROM feeds WHERE url=?;`
	row := a.db.QueryRow(sqlStatement, feedUrl)

	feedItem := feedStruct{}
	err := row.Scan(&feedItem.Pub, &feedItem.Sec, &feedItem.Url)
	if err != nil {
		log.Println("[INFO] Feed not found in DB")
	}
	return &feedItem
}

func checkValidFeedSource(feedUrl string) (*feedStruct, error) {
	log.Println("[DEBUG] Trying to find feed at", feedUrl)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fp := gofeed.NewParser()
	feed, err := fp.ParseURLWithContext(feedUrl, ctx)
	feedItem := feedStruct{}

	if err != nil {
		log.Println("[ERROR] Not a valid feed source")
		return &feedItem, err
	}
	// FIXME! That needs proper error handling.
	feedItem.Url = feedUrl
	feedItem.Title = feed.Title
	feedItem.Description = feed.Description
	feedItem.Link = feed.Link
	if feed.Image != nil {
		feedItem.Image = feed.Image.URL
	} else {
		feedItem.Image = fetchFavicon(feedUrl)
		if feedItem.Image == defaultFeedImage {
			log.Println("[DEBUG] No favicon found for", feedUrl, "using default image")
		} else {
			log.Println("[DEBUG] Using favicon for", feedUrl, ":", feedItem.Image)
		}
	}
	feedItem.Posts = feed.Items

	return &feedItem, err
}

func (a *Atomstr) addSource(feedUrl string) (*feedStruct, error) {
	// var feedElem2 *feedStruct
	feedItem, err := checkValidFeedSource(feedUrl)
	// if feedItem.Title == "" {
	if err != nil {
		log.Println("[ERROR] No valid feed found on", feedUrl)
		return feedItem, err
	}

	// check for existing feed
	feedTest := a.dbGetFeed(feedUrl)
	if feedTest.Url != "" {
		log.Println("[WARN] Feed already exists")
		return feedItem, err
	}

	feedItemKeys := generateKeysForUrl(feedUrl)
	feedItem.Pub = feedItemKeys.Pub
	feedItem.Sec = feedItemKeys.Sec
	// fmt.Println(feedItem)

	a.dbWriteFeed(feedItem)
	if noPub == false {
		nostrUpdateFeedMetadata(feedItem)
	}

	log.Println("[INFO] Parsing post history of new feed")
	for i := range feedItem.Posts {
		processFeedPost(*feedItem, feedItem.Posts[i], historyInterval)
	}
	log.Println("[INFO] Finished parsing post history of new feed")

	return feedItem, err
}

func (a *Atomstr) deleteSource(feedUrl string) bool {
	// check for existing feed
	feedTest := a.dbGetFeed(feedUrl)
	if feedTest.Url != "" {
		sqlStatement := `DELETE FROM feeds WHERE url=?;`
		_, err := a.db.Exec(sqlStatement, feedUrl)
		if err != nil {
			log.Println("[WARN] Can't remove feed")
			log.Fatal(err)
		}
		log.Println("[INFO] feed removed")
		return true
	} else {
		log.Println("[WARN] feed not found")
		return false
	}
}

func (a *Atomstr) listFeeds() {
	feeds := a.dbGetAllFeeds()

	for _, feedItem := range *feeds {
		nip19Pub, _ := nip19.EncodePublicKey(feedItem.Pub)
		fmt.Print(nip19Pub + " ")
		fmt.Println(feedItem.Url)
	}
}
