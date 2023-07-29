# atomstr

atomstr is a RSS/Atom gateway to Nostr.

It fetches all sorts of RSS or Atom feeds, generates Nostr profiles for each and posts new entries to given Nostr relay(s). If you have one of these relays in your profile, you can find and subscribe to the feeds.

Although self hosting is preferable (it always is), there's a test instance at [https://atomstr.data.haus](https://atomstr.data.haus) - please don't hammer this too much as it is running next to my desk.

## Features

- Web portal to add feeds
- Automatic NIP-05 verification of profiles
- Parallel scraping of feeds
- Easy installation


## Installation / Configuration

The prefered way to run this is via Docker. Just use the included [docker-compose.yaml](https://git.sr.ht/~psic4t/atomstr/blob/master/docker-compose.yaml) and modify it to your needs. It contains ready to run Traefik labels. You can remove this part, if you are using ngnix or HAproxy.

If you want to compile it yourself just run "make". 


## Configuration

All configuration is done via environment variables. If you don't want this, modify defines.go.

The following variables are available:

- `DB_PATH`, "./atomstr.db"
- `FETCH_INTERVAL` refresh interval for feeds, default "15m"
- `METADATA_INTERVAL` refresh interval for feed name, icon, etc, default "2h"
- `LOG_LEVEL`, "INFO"
- `WEBSERVER_PORT`, "8061"
- `NIP05_DOMAIN` webserver domain, default  "atomstr.data.haus"
- `MAX_WORKERS` max work in paralel. Default "5"
- `RELAYS_TO_PUBLISH_TO` to which relays this server posts to, add more comma separated. Default  "wss://nostr.data.haus"
- `DEFAULT_FEED_IMAGE` if no feed image is found, use this. Default "https://void.cat/d/NDrSDe4QMx9jh6bD9LJwcK"

## CLI Usage

Add a feed:

    docker exec -it atomstr ./atomstr -a https://my.feed.org/rss

List all feeds:

    docker exec -it atomstr ./atomstr -l


Delete a feed:

    docker exec -it atomstr ./atomstr -d https://my.feed.org/rss


## About

Questions? Ideas? File bugs and TODOs through the [issue
tracker](https://todo.sr.ht/~psic4t/atomstr) or send an email to
[~psic4t/public-inbox@todo.sr.ht](mailto:~psic4t/public-inbox@todo.sr.ht)
