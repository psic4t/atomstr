package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr/nip05"
	"github.com/nbd-wtf/go-nostr/nip19"
)

func (a *Atomstr) webMain(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("templates/index.tmpl"))
	feeds, err := a.dbGetAllFeeds()
	if err != nil {
		http.Error(w, "Failed to get feeds", http.StatusInternalServerError)
		return
	}
	data := webIndex{
		Relays:  relaysToPublishTo,
		Feeds:   *feeds,
		Version: atomstrVersion,
	}
	tmpl.Execute(w, data)
}

func (a *Atomstr) webAdd(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("templates/add.tmpl"))
	url := r.FormValue("url")
	feedItem, err := a.addSource(url)

	var status string
	if err != nil {
		status = "No feed found or feed already exists."
	} else {
		// If npub is provided in query params (from async redirect), use it
		// Otherwise, calculate it from the feed
		if npubParam := r.FormValue("npub"); npubParam != "" {
			feedItem.Npub = npubParam
			log.Printf("[INFO] Using npub from query parameter: %s", npubParam)
		} else {
			feedItem.Npub, err = nip19.EncodePublicKey(feedItem.Pub)
			if err != nil {
				log.Fatal("Error encoding public key:", err)
			}
		}
		status = "Success! Check your feed below and open it with your preferred app."
	}
	data := webAddFeed{
		Status: status,
		Feed:   *feedItem,
	}

	tmpl.Execute(w, data)
}

func (a *Atomstr) webNip05(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	name, _ = url.QueryUnescape(name)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var response []byte
	if name != "" && name != "_" {
		feedItem := a.dbGetFeed(name)

		nip05WellKnownResponse := nip05.WellKnownResponse{
			Names: map[string]string{
				name: feedItem.Pub,
			},
			Relays: map[string][]string{
				feedItem.Pub: relaysToPublishTo,
			},
		}
		response, _ = json.Marshal(nip05WellKnownResponse)
		_, _ = w.Write(response)
	}
}

// Job tracking
var (
	jobs      = make(map[string]*asyncJob)
	jobsMutex sync.RWMutex
)

func generateJobID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func (a *Atomstr) webAddAsync(w http.ResponseWriter, r *http.Request) {
	log.Printf("[INFO] webAddAsync called with method: %s", r.Method)
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	feedURL := r.FormValue("url")
	if feedURL == "" {
		response := asyncResponse{Error: "Feed URL is required"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	// Generate job ID
	jobID := generateJobID()

	// Create job
	job := &asyncJob{
		ID:      jobID,
		URL:     feedURL,
		Status:  "processing",
		Message: "Validating feed URL",
	}

	// Store job
	jobsMutex.Lock()
	jobs[jobID] = job
	jobsMutex.Unlock()

	// Start processing in background
	go a.processFeedAsync(job)

	// Return job ID immediately
	response := asyncResponse{JobID: jobID}
	log.Printf("[INFO] Created async job %s for URL: %s", jobID, feedURL)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (a *Atomstr) webAddStatus(w http.ResponseWriter, r *http.Request) {
	log.Printf("[INFO] webAddStatus called for path: %s", r.URL.Path)
	// Extract job ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 {
		http.Error(w, "Job ID required", http.StatusBadRequest)
		return
	}

	jobID := pathParts[2]

	jobsMutex.RLock()
	job, exists := jobs[jobID]
	jobsMutex.RUnlock()

	if !exists {
		response := statusResponse{Status: "failed", Error: "Job not found"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	response := statusResponse{
		Status:  job.Status,
		Message: job.Message,
		URL:     job.FeedURL,
		Npub:    job.Npub,
	}
	log.Printf("[INFO] Returning status response for job %s with npub: %s", jobID, job.Npub)

	if job.Error != "" {
		response.Error = job.Error
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (a *Atomstr) processFeedAsync(job *asyncJob) {
	// Update status: validating feed
	jobsMutex.Lock()
	job.Message = "Validating feed URL"
	jobsMutex.Unlock()

	// Validate feed source
	feedItem, err := checkValidFeedSource(job.URL)
	if err != nil {
		jobsMutex.Lock()
		job.Status = "failed"
		job.Error = "No valid feed found at URL"
		jobsMutex.Unlock()
		return
	}

	// Update status: checking for duplicates
	jobsMutex.Lock()
	job.Message = "Checking for duplicate feeds"
	jobsMutex.Unlock()

	// Check for existing feed
	feedTest := a.dbGetFeed(job.URL)
	if feedTest.URL != "" {
		jobsMutex.Lock()
		job.Status = "failed"
		job.Error = "Feed already exists"
		jobsMutex.Unlock()
		return
	}

	// Update status: generating keys
	jobsMutex.Lock()
	job.Message = "Generating feed keys"
	jobsMutex.Unlock()

	feedItemKeys := generateKeysForURL(job.URL)
	feedItem.Pub = feedItemKeys.Pub
	feedItem.Sec = feedItemKeys.Sec
	feedItem.Npub, err = nip19.EncodePublicKey(feedItem.Pub)
	if err != nil {
		log.Fatal("Error encoding public key:", err)
	}
	log.Printf("[INFO] Generated npub: %s for URL: %s", feedItem.Npub, job.URL)

	// Update status: saving to database
	jobsMutex.Lock()
	job.Message = "Saving feed to database"
	jobsMutex.Unlock()

	if err := a.dbWriteFeed(feedItem); err != nil {
		jobsMutex.Lock()
		job.Status = "failed"
		job.Error = "Failed to save feed to database"
		jobsMutex.Unlock()
		return
	}

	// Update status: publishing metadata
	if !dryRunMode {
		jobsMutex.Lock()
		job.Message = "Publishing feed metadata"
		jobsMutex.Unlock()
		nostrUpdateFeedMetadata(feedItem)
	} else {
		jobsMutex.Lock()
		job.Message = "Dry-run mode: would publish feed metadata"
		jobsMutex.Unlock()
	}

	// Update status: processing history
	jobsMutex.Lock()
	job.Message = "Processing feed history (this may take a while)"
	jobsMutex.Unlock()

	log.Println("[INFO] Parsing post history of new feed")
	for i := range feedItem.Posts {
		processFeedPost(*feedItem, feedItem.Posts[i], historyInterval)
	}
	log.Println("[INFO] Finished parsing post history of new feed")

	// Success
	jobsMutex.Lock()
	job.Status = "completed"
	job.Message = "Feed successfully added"
	job.FeedURL = feedItem.URL
	job.Npub = feedItem.Npub
	log.Printf("[INFO] Job %s completed with npub: %s", job.ID, job.Npub)
	jobsMutex.Unlock()

	// Clean up old jobs after 5 minutes
	go func() {
		time.Sleep(5 * time.Minute)
		jobsMutex.Lock()
		delete(jobs, job.ID)
		jobsMutex.Unlock()
	}()
}

func (a *Atomstr) webserver() {
	http.HandleFunc("/", a.webMain)
	http.HandleFunc("/add", a.webAdd)
	http.HandleFunc("/add-async", a.webAddAsync)
	http.HandleFunc("/add-status/", a.webAddStatus)
	http.HandleFunc("/.well-known/nostr.json", a.webNip05)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	log.Println("[INFO] Starting webserver at port", webserverPort)
	log.Fatal(http.ListenAndServe(":"+webserverPort, nil))
}
