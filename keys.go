package main

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	keyScoreMax      = 100.0
	keyScoreInitial  = 80.0
	keyScoreGain     = 6.0
	keyScoreIdleRate = 2.0
	penaltyAuth      = 60.0
	penaltyRateLimit = 25.0
	penaltyStatus    = 15.0
	penaltyNetwork   = 20.0
)

type keyState struct {
	fp           string
	key          string
	fails        int
	consecFails  int
	successes    int
	cooldownTill time.Time
	score        float64
	lastStatus   int
	lastErr      string
	lastUsed     time.Time
	latency      time.Duration
	requests     uint64
}

type KeyView struct {
	Masked        string  `json:"masked"`
	Score         float64 `json:"score"`
	State         string  `json:"state"`
	CooldownUntil int64   `json:"cooldown_until"`
	Successes     int     `json:"successes"`
	Failures      int     `json:"failures"`
	LastStatus    int     `json:"last_status"`
	LastError     string  `json:"last_error,omitempty"`
	LatencyMS     int64   `json:"latency_ms"`
	Requests      uint64  `json:"requests"`
}

type keyPool struct {
	mu       sync.Mutex
	items    []*keyState
	strategy string
	cooldown time.Duration
	rr       uint64
}

func newKeyPool(keys []string, strategy string, cooldown time.Duration) *keyPool {
	p := &keyPool{strategy: strategy, cooldown: cooldown}
	if p.cooldown <= 0 {
		p.cooldown = 60 * time.Second
	}
	for _, k := range keys {
		if k == "" {
			continue
		}
		p.items = append(p.items, &keyState{fp: keyFingerprint(k), key: k, score: keyScoreInitial})
	}
	return p
}

func (p *keyPool) size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.items)
}

// availableCount 统计当前不在冷却中的 key 数量（判断该账号池还有没有别号可切）
func (p *keyPool) availableCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	n := 0
	for _, it := range p.items {
		if it.cooldownTill.IsZero() || !now.Before(it.cooldownTill) {
			n++
		}
	}
	return n
}

// cool 立即冷却某个 key：用于账号级不可用（额度耗尽 / 被站点封禁）。
// 与 report 的区别是无需连续失败两次，也不看 HTTP 状态码——渠道适配器已经
// 在响应体里识别出「积分用完」这类业务错误，必须当场换号。
func (p *keyPool) cool(st *keyState, d time.Duration, reason string) {
	if st == nil {
		return
	}
	if d <= 0 {
		d = 15 * time.Minute
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	st.lastUsed = time.Now()
	st.lastStatus = 0
	st.lastErr = reason
	st.fails++
	st.consecFails++
	st.score = 0
	st.cooldownTill = time.Now().Add(d)
}

func (p *keyPool) pick() (*keyState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.items) == 0 {
		return nil, true
	}
	now := time.Now()
	available := make([]*keyState, 0, len(p.items))
	for _, it := range p.items {
		if !it.lastUsed.IsZero() && now.Sub(it.lastUsed) >= time.Minute {
			idle := float64(int(now.Sub(it.lastUsed) / time.Minute))
			it.score += keyScoreIdleRate * idle
			if it.score > keyScoreMax {
				it.score = keyScoreMax
			}
			it.lastUsed = now
		}
		if it.cooldownTill.IsZero() || !now.Before(it.cooldownTill) {
			it.cooldownTill = time.Time{}
			available = append(available, it)
		}
	}
	if len(available) == 0 {
		return nil, false
	}

	var chosen *keyState
	if p.strategy == "round_robin" {
		p.rr++
		chosen = available[int(p.rr)%len(available)]
	} else {
		sort.SliceStable(available, func(i, j int) bool {
			if available[i].score != available[j].score {
				return available[i].score > available[j].score
			}
			return available[i].lastUsed.Before(available[j].lastUsed)
		})
		chosen = available[0]
	}
	chosen.lastUsed = now
	chosen.requests++
	return chosen, true
}

func (p *keyPool) report(st *keyState, status int, latency time.Duration, errMsg string) {
	if st == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	st.lastStatus = status
	st.lastUsed = time.Now()
	if latency > 0 {
		if st.latency == 0 {
			st.latency = latency
		} else {
			st.latency = (st.latency*7 + latency*3) / 10
		}
	}

	if errMsg == "" && status >= 200 && status < 400 {
		st.successes++
		st.consecFails = 0
		st.lastErr = ""
		st.score += keyScoreGain
		if st.score > keyScoreMax {
			st.score = keyScoreMax
		}
		return
	}

	st.fails++
	st.consecFails++
	st.lastErr = errMsg

	penalty := penaltyStatus
	cooldown := p.cooldown
	switch {
	case status == 401 || status == 403:
		penalty = penaltyAuth
		cooldown = p.cooldown * 5
	case status == 429:
		penalty = penaltyRateLimit
	case status == 0:
		penalty = penaltyNetwork
	default:
		if status >= 500 {
			penalty = penaltyStatus
		}
	}
	st.score -= penalty
	if st.score < 0 {
		st.score = 0
	}
	if st.consecFails >= 2 {
		st.cooldownTill = time.Now().Add(cooldown)
	}
}

func (p *keyPool) snapshot() []KeyView {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	out := make([]KeyView, 0, len(p.items))
	for _, it := range p.items {
		view := KeyView{
			Masked:     maskKey(it.key),
			Score:      float64(int(it.score*10)) / 10,
			Successes:  it.successes,
			Failures:   it.fails,
			LastStatus: it.lastStatus,
			LastError:  it.lastErr,
			LatencyMS:  it.latency.Milliseconds(),
			Requests:   it.requests,
		}
		if !it.cooldownTill.IsZero() && now.Before(it.cooldownTill) {
			view.State = "cooling"
			view.CooldownUntil = it.cooldownTill.Unix()
		} else if it.score >= 60 {
			view.State = "ok"
		} else if it.score >= 30 {
			view.State = "degraded"
		} else {
			view.State = "bad"
		}
		out = append(out, view)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func maskKey(k string) string {
	if k == "" {
		return "(无)"
	}
	if strings.Contains(k, "=") && strings.Contains(k, ";") {
		return "ck-" + keyFingerprint(k)[:6]
	}
	if len(k) <= 2 {
		return strings.Repeat("*", len(k))
	}
	if len(k) <= 8 {
		return k[:2] + "…" + k[len(k)-2:]
	}
	if len(k) <= 12 {
		return k[:3] + "…" + k[len(k)-3:]
	}
	return k[:4] + "…" + k[len(k)-4:]
}

func keyFingerprint(k string) string {
	sum := sha256.Sum256([]byte(k))
	return hex.EncodeToString(sum[:8])
}

type keyPersist struct {
	FP            string  `json:"fp"`
	Score         float64 `json:"score"`
	Successes     int     `json:"successes"`
	Failures      int     `json:"failures"`
	ConsecFails   int     `json:"consec_fails"`
	CooldownUntil int64   `json:"cooldown_until"`
	LastStatus    int     `json:"last_status"`
	LastErr       string  `json:"last_error,omitempty"`
	LastUsedMS    int64   `json:"last_used_ms"`
	LatencyMS     int64   `json:"latency_ms"`
	Requests      uint64  `json:"requests"`
}

func (p *keyPool) export() []keyPersist {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]keyPersist, 0, len(p.items))
	for _, it := range p.items {
		item := keyPersist{
			FP:          it.fp,
			Score:       it.score,
			Successes:   it.successes,
			Failures:    it.fails,
			ConsecFails: it.consecFails,
			LastStatus:  it.lastStatus,
			LastErr:     it.lastErr,
			LatencyMS:   it.latency.Milliseconds(),
			Requests:    it.requests,
		}
		if !it.cooldownTill.IsZero() {
			item.CooldownUntil = it.cooldownTill.Unix()
		}
		if !it.lastUsed.IsZero() {
			item.LastUsedMS = it.lastUsed.UnixMilli()
		}
		out = append(out, item)
	}
	return out
}

func (p *keyPool) restore(items []keyPersist) int {
	if len(items) == 0 {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	applied := 0
	for _, saved := range items {
		for _, it := range p.items {
			if it.fp != saved.FP {
				continue
			}
			it.score = saved.Score
			if it.score < 0 {
				it.score = 0
			}
			if it.score > keyScoreMax {
				it.score = keyScoreMax
			}
			it.successes = saved.Successes
			it.fails = saved.Failures
			it.consecFails = saved.ConsecFails
			it.lastStatus = saved.LastStatus
			it.lastErr = saved.LastErr
			it.requests = saved.Requests
			it.latency = time.Duration(saved.LatencyMS) * time.Millisecond
			if saved.CooldownUntil > 0 {
				it.cooldownTill = time.Unix(saved.CooldownUntil, 0)
			}
			if saved.LastUsedMS > 0 {
				it.lastUsed = time.UnixMilli(saved.LastUsedMS)
			}
			applied++
			break
		}
	}
	return applied
}
