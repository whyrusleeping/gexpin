package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"sync"

	api "github.com/ipfs/go-ipfs-api"
)

type pkgInfo struct {
	Url     string
	Hash    string
	Version string
}

var recentlk sync.Mutex
var recent map[string]pkgInfo

var lk sync.Mutex

var log *os.File

const pinlogFile = "pinlogs"

func init() {
	_, err := os.Stat(pinlogFile)
	if os.IsNotExist(err) {
		fi, err := os.Create(pinlogFile)
		if err != nil {
			panic(err)
		}

		log = fi
	} else {
		fi, err := os.OpenFile(pinlogFile, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			panic(err)
		}
		log = fi
	}

	recent = make(map[string]pkgInfo)
}

func logPin(ghurl, ref, vers string) error {
	lk.Lock()
	defer lk.Unlock()
	_, err := fmt.Fprintf(log, "%s %s %s\n", ghurl, vers, ref)
	return err
}

func getExternalIP() (string, error) {
	resp, err := http.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	out, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(out), nil
}

func main() {
	myip, err := getExternalIP()
	if err != nil {
		fmt.Println("error getting external ip: ", err)
		os.Exit(1)
	}

	sh := api.NewShell("localhost:5001")

	http.HandleFunc("/pin_package", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(403)
			return
		}

		ghurl := r.FormValue("ghurl")
		ghurl = strings.Replace(ghurl, "http://", "", 1)
		ghurl = strings.Replace(ghurl, "https://", "", 1)
		if !strings.HasPrefix(ghurl, "github.com/") {
			http.Error(w, "not a github url!", 400)
			return
		}

		userpkg := strings.Replace(ghurl, "github.com/", "", 1)

		template := "https://raw.githubusercontent.com/%s/master/.gx/lastpubver"
		url := fmt.Sprintf(template, userpkg)
		resp, err := http.Get(url)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		defer resp.Body.Close()

		out, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		fields := strings.Fields(string(out))
		if len(fields) != 2 {
			http.Error(w, "incorrectly formatted lastpubver in repo", 400)
			return
		}
		vers := fields[0]
		hash := fields[1]

		flusher := w.(http.Flusher)

		fmt.Fprintln(w, "<!DOCTYPE html>")
		fmt.Fprintf(w, "<p>pinning %s version %s: %s</p><br>", ghurl, vers, hash)
		flusher.Flush()
		refs, err := sh.Refs(hash, true)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		fmt.Fprintln(w, "<ul>")
		for ref := range refs {
			fmt.Fprintf(w, "<li>%s</li>", ref)
			flusher.Flush()
		}
		fmt.Fprintln(w, "</ul>")

		fmt.Fprintln(w, "<p>fetched all deps!<br>calling pin now...</p>")
		flusher.Flush()

		err = sh.Pin(hash)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		err = logPin(ghurl, hash, vers)
		if err != nil {
			http.Error(w, fmt.Sprintf("writing log file: %s", err), 500)
			return
		}

		fmt.Fprintln(w, "<p>success!</p>")
		fmt.Fprintln(w, "<a href='/'>back</a>")

		recentlk.Lock()
		recent[ghurl] = pkgInfo{
			Url:     ghurl,
			Hash:    hash,
			Version: vers,
		}
		recentlk.Unlock()
	})

	http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if sh.IsUp() {
			fmt.Fprintf(w, "gexpin ipfs daemon is online!")
		} else {
			fmt.Fprintf(w, "gexpin ipfs daemon appears to be down. poke @whyrusleeping")
		}
	})

	http.HandleFunc("/node_addr", func(w http.ResponseWriter, r *http.Request) {
		myid, err := sh.ID()
		if err != nil {
			http.Error(w, err.Error(), 503)
			return
		}

		fmt.Fprintf(w, "/ip4/%s/tcp/4001/ipfs/%s", myip, myid.ID)
	})

	http.HandleFunc("/recent", func(w http.ResponseWriter, r *http.Request) {
		recentlk.Lock()
		var pkgs []pkgInfo
		for _, p := range recent {
			pkgs = append(pkgs, p)
		}
		recentlk.Unlock()
		enc := json.NewEncoder(w)
		err := enc.Encode(pkgs)
		if err != nil {
			fmt.Println("json err: ", err)
			return
		}

	})

	h := http.FileServer(http.Dir("."))
	http.Handle("/", h)

	fmt.Printf("Listening on :9444\n")
	http.ListenAndServe(":9444", nil)
}
