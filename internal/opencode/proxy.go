package opencode

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

const (
	proxyListURL = "https://cdn.jsdelivr.net/gh/proxifly/free-proxy-list@main/proxies/protocols/socks5/data.txt"
	upstreamURL  = "https://opencode.ai/zen/v1/chat/completions"
	pingTimeout  = 5 * time.Second
)

type pingedProxy struct {
	addr    string
	latency time.Duration
}

func TransportFor(addr string) *http.Transport {
	var auth *proxy.Auth
	var proxyAddr string

	if strings.Contains(addr, "@") {
		parts := strings.SplitN(addr, "@", 2)
		userPass := strings.SplitN(parts[0], ":", 2)
		if len(userPass) == 2 {
			auth = &proxy.Auth{User: userPass[0], Password: userPass[1]}
		}
		proxyAddr = parts[1]
	} else {
		proxyAddr = addr
	}

	d, err := proxy.SOCKS5("tcp", proxyAddr, auth, proxy.Direct)
	if err != nil {
		return nil
	}
	ctxD, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil
	}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return ctxD.DialContext(ctx, network, addr)
		},
		ResponseHeaderTimeout: 5 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	}
}

func DirectTransport() *http.Transport {
	return &http.Transport{
		ResponseHeaderTimeout: 5 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	}
}

func FetchProxies() []string {
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Get(proxyListURL)
	if err != nil {
		log.Printf("opencode proxy: fetch list: %v", err)
		return nil
	}
	defer resp.Body.Close()

	var addrs []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		u, err := url.Parse(line)
		if err != nil {
			continue
		}
		if u.Host != "" {
			addrs = append(addrs, u.Host)
		}
	}
	return addrs
}

func pingProxy(addr string, ch chan<- pingedProxy) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, pingTimeout)
	if err != nil {
		return
	}
	conn.Close()
	ch <- pingedProxy{addr: addr, latency: time.Since(start)}
}

func pingSortedProxies(addrs []string) []pingedProxy {
	ch := make(chan pingedProxy, len(addrs))
	for _, addr := range addrs {
		go pingProxy(addr, ch)
	}

	timeout := time.After(pingTimeout)
	var results []pingedProxy
	for i := 0; i < len(addrs); i++ {
		select {
		case p := <-ch:
			results = append(results, p)
		case <-timeout:
			goto done
		}
	}
done:
	sort.Slice(results, func(i, j int) bool {
		return results[i].latency < results[j].latency
	})
	return results
}

var (
	mu          sync.Mutex
	cachedAddrs []pingedProxy
	lastWorking *pingedProxy
)

func fetchPingSorted() []pingedProxy {
	mu.Lock()
	defer mu.Unlock()

	if len(cachedAddrs) > 0 {
		return cachedAddrs
	}

	log.Printf("opencode: fetching SOCKS5 list...")
	raw := FetchProxies()
	if len(raw) == 0 {
		return nil
	}
	log.Printf("opencode: pinging %d proxies...", len(raw))
	cachedAddrs = pingSortedProxies(raw)
	lastWorking = nil
	log.Printf("opencode: %d proxies alive", len(cachedAddrs))
	return cachedAddrs
}

func invalidateCache() {
	mu.Lock()
	cachedAddrs = nil
	lastWorking = nil
	mu.Unlock()
}

func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	body, _ := io.ReadAll(r.Body)
	r.Body.Close()

	var reqMap = make(map[string]any)
	json.Unmarshal(body, &reqMap)
	if model, ok := reqMap["model"]; ok {
		log.Printf("opencode: model=%v", model)
	}
	body, _ = json.Marshal(reqMap)

	tryRequest := func(tr *http.Transport, label string) (*http.Response, error) {
		up, _ := http.NewRequestWithContext(r.Context(), "POST", upstreamURL, bytes.NewReader(body))
		up.Header.Set("Content-Type", "application/json")
		up.Header.Set("Authorization", "Bearer public")
		up.Header.Set("x-opencode-client", "desktop")
		up.ContentLength = int64(len(body))
		return tr.RoundTrip(up)
	}

	writeResponse := func(resp *http.Response) {
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}

	mu.Lock()
	sticky := lastWorking
	mu.Unlock()

	if sticky != nil {
		tr := TransportFor(sticky.addr)
		if tr != nil {
			resp, err := tryRequest(tr, "socks5:"+sticky.addr)
			if err == nil && resp.StatusCode != 429 && resp.StatusCode < 500 {
				log.Printf("opencode sticky %s (%v) OK", sticky.addr, sticky.latency)
				writeResponse(resp)
				return
			}
			if err != nil {
				log.Printf("opencode sticky %s: %v — fallback", sticky.addr, err)
			} else {
				resp.Body.Close()
				log.Printf("opencode sticky %s: HTTP %d — fallback", sticky.addr, resp.StatusCode)
			}
		}
	}

	sorted := fetchPingSorted()
	if len(sorted) > 0 {
		for _, p := range sorted {
			tr := TransportFor(p.addr)
			if tr == nil {
				continue
			}
			resp, err := tryRequest(tr, "socks5:"+p.addr)
			if err != nil {
				log.Printf("opencode proxy %s (%v): %v", p.addr, p.latency, err)
				continue
			}
			if resp.StatusCode == 429 || resp.StatusCode >= 500 {
				errBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				log.Printf("opencode proxy %s (%v): HTTP %d", p.addr, p.latency, resp.StatusCode)
				if len(errBody) > 0 {
					log.Printf("  body: %s", strings.TrimSpace(string(errBody)))
				}
				continue
			}
			log.Printf("opencode proxy %s (%v) OK", p.addr, p.latency)
			mu.Lock()
			lastWorking = &p
			mu.Unlock()
			writeResponse(resp)
			return
		}
	} else {
		log.Printf("opencode: no proxies fetched")
	}

	log.Printf("opencode: all proxies failed, invalidating cache")
	invalidateCache()
	log.Printf("opencode: trying direct")
	directTr := DirectTransport()
	resp, err := tryRequest(directTr, "direct")
	if err == nil && resp.StatusCode != 429 && resp.StatusCode < 500 {
		log.Printf("opencode: direct OK")
		writeResponse(resp)
		return
	}
	if err != nil {
		log.Printf("opencode direct: %v", err)
	} else {
		resp.Body.Close()
		log.Printf("opencode direct: HTTP %d", resp.StatusCode)
	}

	http.Error(w, `{"error":"all proxies and direct failed"}`, 502)
}

func InitPool() {
	log.Printf("opencode: proxy init done (ping on demand)")
}
