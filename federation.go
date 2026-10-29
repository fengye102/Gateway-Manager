package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type peerSummary struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	BaseURL        string `json:"base_url"`
	OK             bool   `json:"ok"`
	Error          string `json:"error,omitempty"`
	LatencyMS      int64  `json:"latency_ms"`
	Version        string `json:"version,omitempty"`
	Uptime         string `json:"uptime,omitempty"`
	Requests       uint64 `json:"requests,omitempty"`
	RuleInjections uint64 `json:"rule_injections,omitempty"`
	RuleDenies     uint64 `json:"rule_denies,omitempty"`
	Upstreams      int    `json:"upstreams,omitempty"`
	Unhealthy      int    `json:"unhealthy,omitempty"`
	Rules          int    `json:"rules,omitempty"`
	Profiles       int    `json:"profiles,omitempty"`
}

func (s *Server) fetchPeer(ctx context.Context, peer FederationPeer, timeout time.Duration) peerSummary {
	out := peerSummary{ID: peer.ID, Name: peer.Name, BaseURL: peer.BaseURL}
	if out.Name == "" {
		out.Name = peer.ID
	}
	client := &http.Client{Timeout: timeout}
	target := strings.TrimRight(peer.BaseURL, "/") + "/admin/api/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if peer.Token != "" {
		req.Header.Set("X-Admin-Token", peer.Token)
	}
	started := time.Now()
	resp, err := client.Do(req)
	out.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		out.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return out
	}
	payload := map[string]any{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		out.Error = "响应不是 JSON"
		return out
	}
	out.OK = true
	out.Version = jsonStringField(payload, "version")
	out.Uptime = jsonStringField(payload, "uptime")
	if stats, ok := payload["stats"].(map[string]any); ok {
		out.Requests = uint64(jsonIntField(stats, "requests"))
		out.RuleInjections = uint64(jsonIntField(stats, "rule_inject"))
		out.RuleDenies = uint64(jsonIntField(stats, "rule_deny"))
	}
	if list, ok := payload["upstreams"].([]any); ok {
		out.Upstreams = len(list)
		for _, item := range list {
			if up, ok := item.(map[string]any); ok {
				if healthy, ok := up["healthy"].(bool); ok && !healthy {
					out.Unhealthy++
				}
			}
		}
	}
	if list, ok := payload["rules"].([]any); ok {
		out.Rules = len(list)
	}
	if list, ok := payload["profiles"].([]any); ok {
		out.Profiles = len(list)
	}
	return out
}

func (s *Server) federationSnapshot(ctx context.Context) map[string]any {
	cfg := s.store.Config()
	timeout := time.Duration(cfg.Federation.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	peers := append([]FederationPeer(nil), cfg.Federation.Peers...)
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })

	type result struct {
		idx  int
		data peerSummary
	}
	ch := make(chan result, len(peers))
	for i, peer := range peers {
		go func(idx int, p FederationPeer) {
			ch <- result{idx: idx, data: s.fetchPeer(ctx, p, timeout)}
		}(i, peer)
	}
	out := make([]peerSummary, len(peers))
	for i := 0; i < len(peers); i++ {
		r := <-ch
		out[r.idx] = r.data
	}
	healthy := 0
	var totalRequests uint64
	for _, item := range out {
		if item.OK {
			healthy++
		}
		totalRequests += item.Requests
	}
	return map[string]any{
		"enabled":       cfg.Federation.Enabled,
		"self":          map[string]any{"version": version, "bind": cfg.Server.Bind, "port": cfg.Server.Port},
		"peers":         out,
		"peer_count":    len(out),
		"peer_healthy":  healthy,
		"peer_requests": totalRequests,
	}
}
