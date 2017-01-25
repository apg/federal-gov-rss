package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	Title = "Federal Government 2017"
	Desc  = "Summaries of events from the US Government."
	URL   = "http://jlord.us/federal-gov/"

	sourceFmt = "https://docs.google.com/spreadsheets/d/%s/export?format=csv"

	NumEntries = 20
)

type sheet2rss struct {
	url string

	mu     *sync.Mutex
	ready  bool
	cached []byte
	digest string
}

type rss struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel *channel
}

type channel struct {
	XMLName     xml.Name `xml:"channel"`
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	Description string   `xml:"description"`
	Items       []*item
}

type item struct {
	XMLName     xml.Name `xml:"item"`
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	Description string   `xml:"description"`
	PubDate     string   `xml:"pubDate,omitempty"`
	Category    string   `xml:"category,omitempty"`
}

func parseDate(s string) time.Time {
	for _, x := range []string{
		time.RFC3339, time.RFC3339Nano, time.Stamp,
		time.RFC822, time.RFC822Z, "1/2/2006", "01/02/2006",
	} {
		if p, err := time.Parse(x, s); err == nil {
			return p.UTC()
		}
	}
	return time.Now().UTC()
}

func (e *item) fromRecord(headers, fields []string) {
	get := func(i int) string {
		if i < len(fields) {
			return fields[i]
		}
		return ""
	}
	var cats []string
	for i, h := range headers {
		switch h {
		case "date":
			d := parseDate(get(i))
			e.PubDate = d.Format(time.RFC1123Z)
		case "description":
			e.Title = get(i)
		case "article":
			e.Link = get(i)
		case "activity", "branch":
			cats = append(cats, get(i))
		case "detail":
			e.Description = get(i)
		}
	}
	e.Category = strings.Join(cats, ",")
}

func (rs *rss) fromCSV(r io.Reader, maxRecords int) error {
	cr := csv.NewReader(r)
	records, err := cr.ReadAll()
	if err != nil {
		return err
	}

	if len(records) <= 1 {
		return errors.New("no records")
	}

	headers := records[0]
	start := len(records) - maxRecords
	if start <= 0 {
		start = 1
	}

	out := make([]*item, 0, len(records)-start)
	for i := len(records) - 1; i > start; i-- {
		e := new(item)
		e.fromRecord(headers, records[i])
		out = append(out, e)
	}

	rs.Version = "2.0"
	rs.Channel = &channel{
		Title:       Title,
		Link:        URL,
		Description: Desc,
		Items:       out,
	}

	return nil
}

func (s *sheet2rss) get() (io.Reader, error) {
	resp, err := http.Get(s.url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	return bytes.NewBuffer(body), err
}

func (s *sheet2rss) refresh() {
	r, err := s.get()
	if err != nil {
		log.Fatal(err)
	}

	feed := new(rss)
	err = feed.fromCSV(r, NumEntries)
	if err != nil {
		log.Fatal(err)
	}

	content, err := xml.Marshal(feed)
	if err != nil {
		log.Fatal(err)
	}

	// generate a hash of content
	hash := sha1.Sum(content)
	s.digest = fmt.Sprintf("% x", hash)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached = content
	s.ready = true
}

func (s *sheet2rss) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var content []byte

	s.mu.Lock()
	if s.ready {
		content = s.cached
	}
	s.mu.Unlock()

	if len(content) > 0 {
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8" ?>`))
		w.Write(content)
	} else {
		w.WriteHeader(http.StatusNotFound)
	}
}

func main() {
	handler := &sheet2rss{
		url: fmt.Sprintf(sourceFmt, os.Getenv("SPREADSHEET_KEY")),
		mu:  new(sync.Mutex),
	}
	go handler.refresh()

	http.Handle("/rss", handler)
	log.Fatal(http.ListenAndServe(":"+os.Getenv("PORT"), nil))
}
