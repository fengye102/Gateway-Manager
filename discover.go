package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	discoverDialTimeout = 300 * time.Millisecond
	discoverHTTPTimeout = 2500 * time.Millisecond
)

type FoundUpstream struct {
	Port      int      `json:"port"`
	BaseURL   string   `json:"base_url"`
	Models    []string `json:"models"`
	ElapsedMS int64    `json:"elapsed_ms"`
	Note      string   `json:"note,omitempty"`
}

func PriorityPorts() []int {
	return []int{7878, 7777, 8000, 8080, 8081, 8090, 8888, 9000, 9090, 3000, 5000, 1990, 11434}
}

func PortRange(from, to int) []int {
	if from < 1 {
		from = 1
	}
	if to > 65535 {
		to = 65535
	}
	out := make([]int, 0, to-from+1)
	for p := from; p <= to; p++ {
		out = append(out, p)
	}
	return out
}

func Discover(ctx context.Context, ports []int, workers int) []FoundUpstream {
	if workers <= 0 {
		workers = 64
	}
	jobs := make(chan int)
	results := make(chan FoundUpstream, len(ports))
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for port := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if found, ok := probePort(ctx, port); ok {
					results <- found
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, p := range ports {
			select {
			case <-ctx.Done():
				return
			case jobs <- p:
			}
		}
	}()

	wg.Wait()
	close(results)

	out := make([]FoundUpstream, 0, len(results))
	for item := range results {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

func probePort(ctx context.Context, port int) (FoundUpstream, bool) {
	started := time.Now()
	addr := "127.0.0.1:" + strconv.Itoa(port)
	conn, err := net.DialTimeout("tcp", addr, discoverDialTimeout)
	if err != nil {
		return FoundUpstream{}, false
	}
	_ = conn.Close()

	client := &http.Client{Timeout: discoverHTTPTimeout}
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	models, err := probeModels(ctx, client, base)
	elapsed := time.Since(started).Milliseconds()
	if err != nil {
		return FoundUpstream{}, false
	}
	note := ""
	if len(models) == 0 {
		note = "端口开放但未返回模型列表"
	}
	return FoundUpstream{
		Port:      port,
		BaseURL:   base,
		Models:    models,
		ElapsedMS: elapsed,
		Note:      note,
	}, true
}

func probeModels(ctx context.Context, client *http.Client, base string) ([]string, error) {
	var lastErr error
	for _, path := range []string{"/v1/models", "/models"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		models, err := parseModelList(resp)
		resp.Body.Close()
		if err == nil {
			return models, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("未发现 OpenAI 兼容接口")
	}
	return nil, lastErr
}

func FormatFound(list []FoundUpstream) string {
	if len(list) == 0 {
		return "未发现本地网关"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("发现 %d 个候选网关：\n", len(list)))
	for _, item := range list {
		models := "（模型列表为空）"
		if len(item.Models) > 0 {
			shown := item.Models
			if len(shown) > 6 {
				shown = shown[:6]
			}
			models = strings.Join(shown, ", ")
			if len(item.Models) > len(shown) {
				models += fmt.Sprintf(" …共 %d 个", len(item.Models))
			}
		}
		b.WriteString(fmt.Sprintf("  :%-5d %-24s %4dms  %s %s\n",
			item.Port, item.BaseURL, item.ElapsedMS, models, item.Note))
	}
	return b.String()
}
