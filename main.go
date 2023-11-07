package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func (a *Atomstr) startWorkers(work string) {
	feeds := a.dbGetAllFeeds()
	if len(*feeds) == 0 {
		log.Println("[WARN] No feeds found")
	}

	log.Println("[INFO] Start", work)

	ch := make(chan feedStruct)
	wg := sync.WaitGroup{}

	// start the workers
	for t := 0; t < maxWorkers; t++ {
		wg.Add(1)
		switch work {
		case "metadata":
			go a.processFeedMetadata(ch, &wg)
		default:
			go processFeedUrl(ch, &wg)
		}
	}

	// push the lines to the queue channel for processing
	for _, feedItem := range *feeds {
		ch <- feedItem
	}

	close(ch) // this will cause the workers to stop and exit their receive loop
	wg.Wait() // make sure they all exit
	log.Println("[INFO] Stop", work)
}

func main() {
	a := &Atomstr{db: dbInit()}

	logger()

	feedNew := flag.String("a", "", "Add a new URL to scrape")
	feedDelete := flag.String("d", "", "Remove a feed from db")
	flag.Bool("l", false, "List all feeds with npubs")
	flag.Bool("v", false, "Shows version")
	flag.Parse()
	flagset := make(map[string]bool) // map for flag.Visit. get bools to determine set flags
	flag.Visit(func(f *flag.Flag) { flagset[f.Name] = true })

	if flagset["a"] {
		a.addSource(*feedNew)
	} else if flagset["l"] {
		a.listFeeds()
	} else if flagset["d"] {
		a.deleteSource(*feedDelete)
	} else if flagset["v"] {
		log.Println("[INFO] atomstr version ", atomstrversion)
	} else {
		log.Println("[INFO] Starting atomstr v", atomstrversion)
		//slog.Info("Starting atomstr v", atomstrversion)
		go a.webserver()

		// first run
		a.startWorkers("metadata")
		a.startWorkers("scrape")

		metadataTicker := time.NewTicker(metadataInterval)
		updateTicker := time.NewTicker(fetchInterval)

		cancelChan := make(chan os.Signal, 1)
		// catch SIGETRM or SIGINTERRUPT
		signal.Notify(cancelChan, syscall.SIGTERM, syscall.SIGINT)

		go func() {
			for {
				select {
				case <-metadataTicker.C:
					a.startWorkers("metadata")
				case <-updateTicker.C:
					a.startWorkers("scrape")
				}
			}
		}()
		sig := <-cancelChan

		log.Println("[DEBUG] Caught signal %v", sig)
		metadataTicker.Stop()
		updateTicker.Stop()
		log.Println("[INFO] Closing DB")
		a.db.Close()
		log.Println("[INFO] Shutting down")

	}
}
