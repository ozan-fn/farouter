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
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

const (
	proxyListURL = "https://cdn.jsdelivr.net/gh/proxifly/free-proxy-list@main/proxies/protocols/socks5/data.txt"
	upstreamURL  = "https://opencode.ai/zen/v1/chat/completions"
)

var webshareProxies = []string{
	"kauhwjxz-1:nz8hufnch0pg@p.webshare.io:1080",
	"kauhwjxz-2:nz8hufnch0pg@p.webshare.io:1080",
	"kauhwjxz-3:nz8hufnch0pg@p.webshare.io:1080",
	"kauhwjxz-4:nz8hufnch0pg@p.webshare.io:1080",
	"kauhwjxz-5:nz8hufnch0pg@p.webshare.io:1080",
	"kauhwjxz-6:nz8hufnch0pg@p.webshare.io:1080",
	"kauhwjxz-7:nz8hufnch0pg@p.webshare.io:1080",
	"kauhwjxz-8:nz8hufnch0pg@p.webshare.io:1080",
	"kauhwjxz-9:nz8hufnch0pg@p.webshare.io:1080",
	"kauhwjxz-10:nz8hufnch0pg@p.webshare.io:1080",
}

type Pool struct {
	mu           sync.RWMutex
	addrs        []string
	idx          int
	tr           *http.Transport
	webshareIdx  int
	directTried  bool
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

func (p *Pool) PickSticky() (*http.Transport, string) {
	p.mu.RLock()
	tr, addr := p.tr, p.addrs[p.idx]
	p.mu.RUnlock()
	return tr, addr
}

func (p *Pool) Rotate() (*http.Transport, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := 0; i < len(p.addrs); i++ {
		p.idx = (p.idx + 1) % len(p.addrs)
		tr := TransportFor(p.addrs[p.idx])
		if tr != nil {
			p.tr = tr
			log.Printf("opencode proxy: %s (%d/%d)", p.addrs[p.idx], p.idx+1, len(p.addrs))
			return tr, p.addrs[p.idx]
		}
	}
	p.tr = nil
	return nil, ""
}

func (p *Pool) Refetch() {
	newAddrs := FetchProxies()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(newAddrs) > 0 {
		p.addrs = newAddrs
		p.idx = -1
		log.Printf("opencode proxy: refetched %d proxies", len(newAddrs))
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

func Handle(w http.ResponseWriter, r *http.Request, pool *Pool) {
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

	for _, addr := range webshareProxies {
		tr := TransportFor(addr)
		if tr == nil {
			continue
		}
		resp, err := tryRequest(tr, "webshare:"+addr)
		if err != nil {
			log.Printf("opencode webshare %s: %v", addr, err)
			continue
		}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			log.Printf("opencode webshare %s: HTTP %d", addr, resp.StatusCode)
			if len(errBody) > 0 {
				log.Printf("  body: %s", strings.TrimSpace(string(errBody)))
			}
			continue
		}
		log.Printf("opencode: webshare %s OK", addr)
		writeResponse(resp)
		return
	}

	log.Printf("opencode: webshare exhausted, trying direct")
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

	log.Printf("opencode: direct failed, trying SOCKS5 fetch")
	pool.mu.RLock()
	total := len(pool.addrs)
	pool.mu.RUnlock()

	tryProxy := func() (*http.Transport, string) {
		tr, addr := pool.PickSticky()
		if tr == nil {
			tr, addr = pool.Rotate()
			if tr == nil {
				pool.Refetch()
				tr, addr = pool.Rotate()
			}
		}
		return tr, addr
	}

	for attempts := 0; attempts < total+2; attempts++ {
		tr, addr := tryProxy()
		if tr == nil {
			break
		}

		resp, err := tryRequest(tr, "socks5:"+addr)
		if err != nil {
			log.Printf("opencode SOCKS5 %s: %v — rotate", addr, err)
			pool.Rotate()
			continue
		}

		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			log.Printf("opencode SOCKS5 %s: HTTP %d — rotate", addr, resp.StatusCode)
			if len(errBody) > 0 {
				log.Printf("  body: %s", strings.TrimSpace(string(errBody)))
			}
			pool.Rotate()
			continue
		}
		log.Printf("opencode: SOCKS5 %s OK", addr)
		writeResponse(resp)
		return
	}
	http.Error(w, `{"error":"all methods failed"}`, 502)
}

func InitPool() *Pool {
	log.Printf("opencode proxy: fetching SOCKS5 list...")
	addrs := FetchProxies()
	if len(addrs) == 0 {
		log.Fatalf("opencode proxy: no proxies fetched")
	}
	log.Printf("opencode proxy: loaded %d proxies", len(addrs))
	pool := &Pool{addrs: addrs, idx: -1}
	pool.Rotate()
	return pool
}
