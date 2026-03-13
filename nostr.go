package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func nostrUpdateFeedMetadata(feedItem *feedStruct) {
	// fmt.Println(feedItem)

	metadata := map[string]string{
		"name":    feedItem.Title + " (RSS Feed)",
		"about":   feedItem.Description + "\n\n" + feedItem.Link,
		"picture": feedItem.Image,
		"nip05":   feedItem.URL + "@" + nip05Domain, // should this be optional?
	}

	content, _ := json.Marshal(metadata)

	ev := nostr.Event{
		PubKey:    feedItem.Pub,
		CreatedAt: nostr.Now(),
		Kind:      nostr.KindProfileMetadata,
		Tags:      nostr.Tags{},
		Content:   string(content),
	}
	ev.ID = string(ev.Serialize())
	ev.Sign(feedItem.Sec)
	log.Println("[DEBUG] Updating feed metadata for", feedItem.Title)

	if !dryRunMode {
		nostrPostItem(ev)
	} else {
		eventJSON, _ := json.Marshal(ev)
		log.Println("[DRY-RUN] Would publish metadata event:", string(eventJSON))
	}

	nostrPublishRelayList(feedItem)
}

func (a *Atomstr) processFeedMetadata(ch chan feedStruct, wg *sync.WaitGroup) {
	for feedItem := range ch {
		data, err := checkValidFeedSource(feedItem.URL)
		if err != nil {
			log.Println("[ERROR] error updating feed")
			continue
		}
		feedItem.Title = data.Title
		feedItem.Description = data.Description
		feedItem.Link = data.Link
		feedItem.Image = data.Image
		nostrUpdateFeedMetadata(&feedItem)
	}
	wg.Done()
}

func (a *Atomstr) ALTnostrUpdateAllFeedsMetadata() error {
	feeds, err := a.dbGetAllFeeds()
	if err != nil {
		return fmt.Errorf("failed to get feeds: %w", err)
	}

	log.Println("[INFO] Updating feeds metadata")
	for _, feedItem := range *feeds {
		data, err := checkValidFeedSource(feedItem.URL)
		// if data.Title == "" {
		if err != nil {
			log.Println("[ERROR] error updating feed")
			continue
		}
		feedItem.Title = data.Title
		feedItem.Description = data.Description
		feedItem.Link = data.Link
		feedItem.Image = data.Image
		nostrUpdateFeedMetadata(&feedItem)
	}
	log.Println("[INFO] Finished updating feeds metadata")
	return nil
}

func nostrPublishRelayList(feedItem *feedStruct) {
	var tags nostr.Tags
	for _, url := range relaysToPublishTo {
		tags = append(tags, nostr.Tag{"r", url, "write"})
	}

	ev := nostr.Event{
		PubKey:    feedItem.Pub,
		CreatedAt: nostr.Now(),
		Kind:      10002,
		Tags:      tags,
		Content:   "",
	}
	ev.ID = string(ev.Serialize())
	ev.Sign(feedItem.Sec)
	log.Println("[DEBUG] Publishing NIP-65 relay list for", feedItem.Title)

	if !dryRunMode {
		nostrPostToRelays(ev, dedupeRelays(relaysToPublishTo, discoveryRelays, blasterRelays))
	} else {
		eventJSON, _ := json.Marshal(ev)
		log.Println("[DRY-RUN] Would publish NIP-65 relay list event:", string(eventJSON))
	}
}

func dedupeRelays(lists ...[]string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, list := range lists {
		for _, url := range list {
			if !seen[url] {
				seen[url] = true
				result = append(result, url)
			}
		}
	}
	return result
}

func nostrPostToRelays(ev nostr.Event, relays []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, url := range relays {
		relay, err := nostr.RelayConnect(ctx, url)
		if err != nil {
			log.Println("[ERROR]", url, err)
			continue
		}
		err = relay.Publish(ctx, ev)
		if err != nil {
			log.Println("[WARN]", url, err)
			continue
		}

		err = relay.Close()
		if err != nil {
			log.Println("[ERROR]", err)
			continue
		}

		log.Printf("[INFO] Event published to %s\n", url)
	}
}

func nostrPostItem(ev nostr.Event) {
	if dryRunMode {
		eventJSON, _ := json.Marshal(ev)
		log.Println("[DRY-RUN] Would publish event to relays:", string(eventJSON))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, url := range relaysToPublishTo {
		relay, err := nostr.RelayConnect(ctx, url)
		if err != nil {
			log.Println("[ERROR]", url, err)
			continue
		}
		err = relay.Publish(ctx, ev)
		if err != nil {
			log.Println("[WARN]", url, err)
			continue
		}

		err = relay.Close()
		if err != nil {
			log.Println("[ERROR]", err)
			continue
		}

		log.Printf("[INFO] Event published to %s\n", url)
	}
}
