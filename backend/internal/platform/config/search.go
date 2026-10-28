package config

import (
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var searchIndexPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

// Search is optional. Invalid or unavailable ES cannot block core API startup.
// This local profile uses no authentication; production hosting is out of scope.
type Search struct {
	URL     string
	Index   string
	Timeout time.Duration
	Ready   bool
	Reason  string
}

func loadSearch(lookup func(string) (string, bool)) Search {
	cfg := Search{Index: "community-notes-v1", Timeout: 3 * time.Second, Reason: "笔记搜索未配置"}
	if raw, ok := lookup("ES_URL"); ok {
		cfg.URL = strings.TrimSpace(raw)
	}
	if cfg.URL == "" {
		return cfg
	}
	cfg.Reason = "笔记搜索配置无效"
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || strings.ContainsAny(u.Host, "\\ \t\r\n") {
		return cfg
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())) {
		return cfg
	}
	if u.Port() != "" {
		if _, err := parseInt("ES_URL port", u.Port(), 1, 65535); err != nil {
			return cfg
		}
	}
	if u.Path != "" && u.Path != "/" {
		return cfg
	}
	if raw, ok := lookup("ES_INDEX"); ok {
		cfg.Index = strings.TrimSpace(raw)
	}
	if !searchIndexPattern.MatchString(cfg.Index) {
		return cfg
	}
	if raw, ok := lookup("ES_TIMEOUT_SECONDS"); ok {
		seconds, err := parseInt("ES_TIMEOUT_SECONDS", raw, 1, 10)
		if err != nil {
			return cfg
		}
		cfg.Timeout = time.Duration(seconds) * time.Second
	}
	cfg.URL = strings.TrimRight(u.String(), "/")
	cfg.Ready = true
	cfg.Reason = "笔记搜索已配置，远端可用性在请求时确认"
	return cfg
}
