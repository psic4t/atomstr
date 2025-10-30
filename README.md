# atomstr

atomstr is a RSS/Atom gateway to Nostr.

It fetches all sorts of RSS or Atom feeds, generates Nostr profiles for each and posts new entries to given Nostr relay(s). If you have one of these relays in your profile, you can find and subscribe to the feeds.

Although self hosting is preferable (it always is), there's a test instance at [https://atomstr.data.haus](https://atomstr.data.haus) - please don't hammer this too much as it is running next to my desk.

## Features

- Web portal to add feeds
- Automatic NIP-05 verification of profiles
- Parallel scraping of feeds
- Feed availability ranking with automatic failure tracking
- Broken feeds are only retried periodically to reduce load
- Visual indicators for feed status in web interface
- Easy installation
- NIP-48 support

## Installation / Configuration

The prefered way to run this is via Docker. Just use the included docker-compose.yaml and modify it to your needs. It contains ready to run Traefik labels. You can remove this part, if you are using ngnix or HAproxy.

If you want to compile it yourself just run "make". 


## Configuration

All configuration is done via environment variables. If you don't want this, modify defines.go.

The following variables are available:

- `DB_PATH`, "./atomstr.db"
- `FETCH_INTERVAL` refresh interval for feeds, default "15m"
- `METADATA_INTERVAL` refresh interval for feed name, icon, etc, default "12h"
- `HISTORY_INTERVAL` history interval for feed initial sync, default "1h"
- `LOG_LEVEL`, "DEBUG"
- `WEBSERVER_PORT`, "8061"
- `NIP05_DOMAIN` webserver domain, default  "atomstr.data.haus"
- `MAX_WORKERS` max work in paralel. Default "5"
- `RELAYS_TO_PUBLISH_TO` to which relays this server posts to, add more comma separated. Default "wss://nostr.data.haus"
- `DEFAULT_FEED_IMAGE` if no feed image is found, use this. Default "https://upload.wikimedia.org/wikipedia/en/thumb/4/43/Feed-icon.svg/256px-Feed-icon.svg.png"
- `MAX_FAILURE_ATTEMPTS` maximum consecutive failures before marking feed as broken, default "3"
- `BROKEN_FEED_RETRY_INTERVAL` how often to retry broken feeds, default "24h"

## Feed Availability Ranking

atomstr automatically tracks feed availability and ranks feeds based on their reliability:

- **Active feeds** (✓): Working normally, fetched regularly
- **Warning feeds** (⚠): Have some failures but still being attempted
- **Broken feeds** (✗): Failed multiple times, only retried once per day

When a feed fails to fetch, atomstr increments a failure counter. After `MAX_FAILURE_ATTEMPTS` consecutive failures, the feed is marked as "broken" and only retried every `BROKEN_FEED_RETRY_INTERVAL`. If a broken feed starts working again, it's automatically restored to active status.

The web interface shows feed status with visual indicators, and broken feeds are displayed in gray to distinguish them from working feeds.

## CLI Usage

Add a feed:

    docker exec -it atomstr ./atomstr -a https://my.feed.org/rss

List all feeds (shows status for broken feeds):

    docker exec -it atomstr ./atomstr -l

Delete a feed:

    docker exec -it atomstr ./atomstr -d https://my.feed.org/rss

Dry Run mode (don't post anything):

    docker exec -it atomstr ./atomstr -dry-run


## About

Questions? Ideas? File bugs and TODOs through the issue
tracker!
