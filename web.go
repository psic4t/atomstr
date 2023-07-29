package main

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"

	"github.com/nbd-wtf/go-nostr/nip05"
	"github.com/nbd-wtf/go-nostr/nip19"
)

func (a *Atomstr) webMain(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("templates/index.tmpl"))
	feeds := a.dbGetAllFeeds()
	data := webIndex{
		Relays:  relaysToPublishTo,
		Feeds:   *feeds,
		Version: atomstrversion,
	}
	tmpl.Execute(w, data)
}

func (a *Atomstr) webAdd(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("templates/add.tmpl"))
	feedItem, err := a.addSource(r.FormValue("url"))

	var status string
	if err != nil {
		status = "No feed found or feed already exists."
	} else {
		feedItem.Npub, _ = nip19.EncodePublicKey(feedItem.Pub)
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

	var response []byte
	if name != "" && name != "_" {
		feedItem := a.dbGetFeed(name)

		nip05WellKnownResponse := nip05.WellKnownResponse{
			Names: map[string]string{
				name: feedItem.Pub,
			},
			Relays: nil,
		}
		response, _ = json.Marshal(nip05WellKnownResponse)
		_, _ = w.Write(response)
	}
}

func (a *Atomstr) webserver() {
	http.HandleFunc("/", a.webMain)
	http.HandleFunc("/add", a.webAdd)
	http.HandleFunc("/.well-known/nostr.json", a.webNip05)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	log.Println("[INFO] Starting webserver at port", webserverPort)
	log.Fatal(http.ListenAndServe(":"+webserverPort, nil))
}
