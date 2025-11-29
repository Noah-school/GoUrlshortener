package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

type URLData struct {
	OriginalURL string `json:"original_url"` // `` are the tags for json
	Clicks      int    `json:"clicks"`
}

type StorageData struct {
	URLs      map[string]URLData `json:"urls"`
	CurrentID uint64             `json:"current_id"`
}

type LogEntry struct {
	Key       string  `json:"key"`
	Data      URLData `json:"data"`
	CurrentID uint64  `json:"current_id"`
}

// Avoids crashes when when saving to map[string]URLData directly
var store = struct {
	sync.RWMutex
	Data StorageData
}{
	Data: StorageData{
		URLs:      make(map[string]URLData),
		CurrentID: 0,
	},
}

// Don't ask me why i did it but its for my hate it was not a O(1) time complexity
// Why because the ID before was random and encrypted thus when checking if it already existed it had to O(N) instead of the current O(1)
// I wasted too much time on this feature (future me just save your time and don't try this again)
// love ==> Knuth's multiplicative hashing
const (
	charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	prime   = uint64(11400714819323198485)
	xorSalt = uint64(214234534534)
)

func obfuscate(id uint64) uint64 {
	return (id * prime) ^ xorSalt
}

func toBase64(id uint64) string {
	if id == 0 {
		return string(charset[0])
	}

	encoded := ""
	base := uint64(len(charset))

	for id > 0 {
		remainder := id % base
		encoded = string(charset[remainder]) + encoded
		id = id / base
	}
	return encoded
}

// Temp save
func appendToLog(shortKey string, data URLData, currentID uint64) {
	f, err := os.OpenFile("transaction.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("Error opening log:", err)
		return
	}
	defer f.Close()

	entry := LogEntry{
		Key:       shortKey,
		Data:      data,
		CurrentID: currentID,
	}

	jsonEntry, _ := json.Marshal(entry)
	f.Write(jsonEntry)
	f.WriteString("\n")
}

func startBackgroundSaver() {
	ticker := time.NewTicker(10 * time.Second)
	for range ticker.C {
		saveData()
	}
}

// Full save
func saveData() {
	store.RLock()
	fileData, err := json.MarshalIndent(store.Data, "", "  ")
	store.RUnlock()

	if err != nil {
		return
	}

	err = os.WriteFile("urls.json.tmp", fileData, 0644)
	if err != nil {
		return
	}

	err = os.Rename("urls.json.tmp", "urls.json")
	if err == nil {
		os.WriteFile("transaction.log", []byte(""), 0644)
	}
}

func loadData() {
	fileData, err := os.ReadFile("urls.json")
	store.Lock()
	defer store.Unlock()

	if err == nil {
		json.Unmarshal(fileData, &store.Data)
	}

	f, err := os.Open("transaction.log")
	if err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		count := 0
		for scanner.Scan() {
			var entry LogEntry
			if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil {
				store.Data.URLs[entry.Key] = entry.Data
				if entry.CurrentID > store.Data.CurrentID {
					store.Data.CurrentID = entry.CurrentID
				}
				count++
			}
		}
		if count > 0 {
			fmt.Printf("Recovered %d entries from transaction log.\n", count)
		}
	}

	fmt.Printf("Server started. Loaded %d URLs.\n", len(store.Data.URLs))
}

func shortenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "invalid request method", http.StatusMethodNotAllowed)
		return
	}

	originalURL := r.FormValue("url")

	u, err := url.ParseRequestURI(originalURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		http.Error(w, "Invalid URL. Must include http:// or https://", http.StatusBadRequest)
		return
	}

	// shortKey := strconv.Itoa(len(store.m) + 1)
	store.Lock()

	store.Data.CurrentID++
	rawID := uint64(store.Data.CurrentID)
	scrambledID := obfuscate(rawID)
	shortKey := toBase64(scrambledID)

	newData := URLData{
		OriginalURL: originalURL,
		Clicks:      0,
	}
	store.Data.URLs[shortKey] = newData

	currentID := store.Data.CurrentID
	store.Unlock()

	// saveData()
	appendToLog(shortKey, newData, currentID)

	fmt.Fprintf(w, "Shortened URL: http://localhost:8080/%s\n", shortKey)
}

func redirectHandler(w http.ResponseWriter, r *http.Request) {
	shortKey := r.URL.Path[1:]
	if shortKey == "" {
		fmt.Fprintf(w, "Welcome to the URL Shortener! POST to /shorten to generate")
		return
	}
	store.Lock()
	data, exists := store.Data.URLs[shortKey]
	var currentID uint64 = 0

	if exists {
		data.Clicks++
		store.Data.URLs[shortKey] = data
		currentID = store.Data.CurrentID
	}
	store.Unlock()

	if exists {
		go appendToLog(shortKey, data, currentID)

		http.Redirect(w, r, data.OriginalURL, http.StatusFound)
	} else {
		http.Error(w, "Short URL not found", http.StatusNotFound)
	}
}

func statsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "URL statistics:\n")
	store.RLock()
	for key, data := range store.Data.URLs {
		fmt.Fprintf(w, "urlID: %s, %s : %d clicks\n", key, data.OriginalURL, data.Clicks)
	}
	store.RUnlock()
}

func main() {
	loadData()

	go startBackgroundSaver()

	http.HandleFunc("/shorten", shortenHandler)
	http.HandleFunc("/", redirectHandler)
	http.HandleFunc("/stats", statsHandler)
	fmt.Println("running server on 8080")

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		panic(err)
	}
}
