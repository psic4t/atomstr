package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func nostrUpdateFeedMetadata(feedItem *feedStruct) {
	//fmt.Println(feedItem)

	metadata := map[string]string{
		"name":    feedItem.Title + " (RSS Feed)",
		"about":   feedItem.Description + "\n\n" + feedItem.Link,
		"picture": feedItem.Image,
		"nip05":   feedItem.Url + "@" + nip05Domain, // should this be optional?
	}

	content, _ := json.Marshal(metadata)

	ev := nostr.Event{
		PubKey:    feedItem.Pub,
		CreatedAt: nostr.Now(),
		Kind:      nostr.KindSetMetadata,
		Tags:      nostr.Tags{},
		Content:   string(content),
	}
	ev.ID = string(ev.Serialize())
	ev.Sign(feedItem.Sec)
	log.Println("[DEBUG] Updating feed metadata for", feedItem.Title)

	if noPub == false {
		nostrPostItem(ev)
	}
}

func (a *Atomstr) processFeedMetadata(ch chan feedStruct, wg *sync.WaitGroup) {
	for feedItem := range ch {
		data, err := checkValidFeedSource(feedItem.Url)
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

func (a *Atomstr) ALTnostrUpdateAllFeedsMetadata() {
	feeds := a.dbGetAllFeeds()

	log.Println("[INFO] Updating feeds metadata")
	for _, feedItem := range *feeds {
		data, err := checkValidFeedSource(feedItem.Url)
		//if data.Title == "" {
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
}

func nostrPostItem(ev nostr.Event) {
	ctx := context.Background()
	//for _, url := range []string{"wss://nostr.data.haus", "wss://nostr-pub.wellorder.net"} {
	for _, url := range relaysToPublishTo {
		relay, err := nostr.RelayConnect(ctx, url)
		if err != nil {
			log.Println("[ERROR]", err)
			continue
		}
		_, err = relay.Publish(ctx, ev)
		if err != nil {
			log.Println("[ERROR]", err)
			continue
		}

		log.Printf("[DEBUG] Event published to %s\n", url)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
}
