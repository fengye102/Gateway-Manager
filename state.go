package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type LogEntry struct {
	Seq     uint64 `json:"seq"`
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type Stats struct {
	Requests       uint64 `json:"requests"`
	Injected       uint64 `json:"injected"`
	NoProfile      uint64 `json:"no_profile"`
	BadShape       uint64 `json:"bad_shape"`
	UpstreamErrors uint64 `json:"upstream_errors"`
	AuthFailures   uint64 `json:"auth_failures"`
	RuleInject     uint64 `json:"rule_inject"`
	RuleDeny       uint64 `json:"rule_deny"`
	KeySkips       uint64 `json:"key_skips"`
	StartedAt      int64  `json:"started_at"`
}

type UpstreamState struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	Enabled       bool        `json:"enabled"`
	Healthy       bool        `json:"healthy"`
	Fails         int         `json:"fails"`
	CooldownUntil int64       `json:"cooldown_until"`
	Inflight      int         `json:"inflight"`
	WindowCount   int         `json:"window_count"`
	Requests      uint64      `json:"requests"`
	Successes     uint64      `json:"successes"`
	Failures      uint64      `json:"failures"`
	LastError     string      `json:"last_error"`
	LastStatus    int         `json:"last_status"`
	Circuit       string      `json:"circuit"`
	LastSeen      int64       `json:"last_seen"`
	LatencyMS     int64       `json:"latency_ms"`
	Models        []string    `json:"models"`
	Weight        int         `json:"weight"`
	Keys          []KeyView   `json:"keys"`
	Budget        *BudgetView `json:"budget,omitempty"`
}

type ReplayEntry struct {
	ID        string            `json:"id"`
	Time      int64             `json:"time"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Model     string            `json:"model"`
	Status    int               `json:"status"`
	Message   string            `json:"message"`
	Rules     []string          `json:"rules"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"`
	BodySize  int               `json:"body_size"`
	Truncated bool              `json:"truncated"`
	Upstream  string            `json:"upstream,omitempty"`
	LatencyMS int64             `json:"latency_ms,omitempty"`
}

type ruleHitState struct {
	Matched uint64 `json:"matched"`
	Skipped uint64 `json:"skipped"`
	Denied  uint64 `json:"denied"`
	LastHit int64  `json:"last_hit"`
}

type persistedState struct {
	Version   int                      `json:"version"`
	SavedAt   int64                    `json:"saved_at"`
	Upstreams map[string][]keyPersist  `json:"upstreams"`
	RuleStats map[string]*ruleHitState `json:"rule_stats"`
}

type budgetState struct {
	Day    string `json:"day"`
	Reqs   uint64 `json:"requests"`
	Tokens uint64 `json:"tokens"`
}

type BudgetView struct {
	Requests    uint64 `json:"requests"`
	Tokens      uint64 `json:"tokens"`
	MaxRequests int    `json:"max_requests"`
	MaxTokens   int    `json:"max_tokens"`
	Exhausted   bool   `json:"exhausted"`
	Day         string `json:"day"`
}

type runtimeState struct {
	mu            sync.Mutex
	fails         int
	circuit       string
	openCount     int
	halfInflight  int
	halfOpened    uint64
	cooldownUntil time.Time
	inflight      int
	windowStart   time.Time
	windowCount   int
	healthy       bool
	models        []string
	lastErr       string
	lastStatus    int
	lastSeen      time.Time
	latency       time.Duration
	requests      uint64
	successes     uint64
	failures      uint64
	keys          *keyPool
	keySig        string
}

type Store struct {
	mu         sync.RWMutex
	cfg        *Config
	cfgPath    string
	statePath  string
	persistOn  bool
	persisted  *persistedState
	rt         map[string]*runtimeState
	logs       []LogEntry
	logSeq     uint64
	stats      Stats
	rotation   map[string]uint64
	ruleHits   map[string]*ruleHitState
	dirty      bool
	lastSave   time.Time
	reqHist    *histogram
	upHist     map[string]*histogram
	respCodes  map[string]map[string]uint64
	kindHits   map[string]uint64
	replay     []ReplayEntry
	replaySeq  uint64
	samples    []ReplayEntry
	sampleSeq  uint64
	sampleTick uint64
	routeHits  map[string]map[string]uint64
	budget     map[string]*budgetState
	tokUsage   map[string]uint64
	mirrorTick uint64
	mirrorOK   map[string]uint64
	mirrorFail map[string]uint64
	expTick    uint64
	expStats   map[string]map[string]*expArmStats
	queueHist  *histogram
	ttfbHist   *histogram
	relayHist  *histogram
}

func NewStore(cfg *Config, cfgPath string) *Store {
	s := &Store{
		cfg:       cfg,
		cfgPath:   cfgPath,
		rt:        map[string]*runtimeState{},
		logs:      make([]LogEntry, 0, 512),
		rotation:  map[string]uint64{},
		stats:     Stats{StartedAt: time.Now().Unix()},
		ruleHits:  map[string]*ruleHitState{},
		persistOn: true,
	}
	s.mu.Lock()
	for _, u := range cfg.Upstreams {
		s.rt[u.ID] = &runtimeState{healthy: true}
	}
	s.mu.Unlock()
	return s
}

func (s *Store) Config() *Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Store) ConfigPath() string { return s.cfgPath }

func (s *Store) SetConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("配置为空")
	}
	normalizeConfig(cfg)
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	s.mu.Lock()
	old := s.rt
	next := map[string]*runtimeState{}
	for _, u := range cfg.Upstreams {
		if st, ok := old[u.ID]; ok {
			next[u.ID] = st
		} else {
			next[u.ID] = &runtimeState{healthy: true}
		}
	}
	s.cfg = cfg
	s.rt = next
	s.dirty = true
	s.mu.Unlock()
	if s.cfgPath != "" {
		return SaveConfig(s.cfgPath, cfg)
	}
	return nil
}

func (s *Store) stateFor(id string) *runtimeState {
	s.mu.RLock()
	st := s.rt[id]
	s.mu.RUnlock()
	if st != nil {
		return st
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.rt[id]; ok {
		return st
	}
	st = &runtimeState{healthy: true}
	s.rt[id] = st
	return st
}

func (s *Store) keyPoolFor(u *Upstream) *keyPool {
	st := s.stateFor(u.ID)
	cfg := s.Config()
	sig := strings.Join(u.APIKeys, "\x00") + "|" + cfg.Server.KeyStrategy + "|" + fmt.Sprint(cfg.Server.KeyCooldownSeconds)
	s.mu.RLock()
	persisted := s.persisted
	s.mu.RUnlock()

	st.mu.Lock()
	if st.keys == nil || st.keySig != sig {
		st.keys = newKeyPool(u.APIKeys, cfg.Server.KeyStrategy, time.Duration(cfg.Server.KeyCooldownSeconds)*time.Second)
		st.keySig = sig
		if persisted != nil && persisted.Upstreams != nil {
			if applied := st.keys.restore(persisted.Upstreams[u.ID]); applied > 0 {
				s.logf("INFO", "已恢复上游 %s 的 %d 个 key 健康状态", u.ID, applied)
			}
		}
	}
	pool := st.keys
	st.mu.Unlock()
	return pool
}

func (s *Store) logf(level string, format string, args ...any) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	s.mu.Lock()
	s.logSeq++
	limit := s.cfg.Server.LogLimit
	if limit <= 0 {
		limit = 500
	}
	entry := LogEntry{
		Seq:     s.logSeq,
		Time:    time.Now().Format("15:04:05"),
		Level:   level,
		Message: msg,
	}
	s.logs = append(s.logs, entry)
	if len(s.logs) > limit {
		drop := len(s.logs) - limit
		s.logs = append([]LogEntry(nil), s.logs[drop:]...)
	}
	s.mu.Unlock()
	fmt.Printf("[%s] %s\n", entry.Time, msg)
}

func (s *Store) Info(format string, args ...any)  { s.logf("INFO", format, args...) }
func (s *Store) Warn(format string, args ...any)  { s.logf("WARN", format, args...) }
func (s *Store) Error(format string, args ...any) { s.logf("ERR", format, args...) }

func (s *Store) Logs(limit int) []LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.logs) {
		limit = len(s.logs)
	}
	out := make([]LogEntry, limit)
	copy(out, s.logs[len(s.logs)-limit:])
	return out
}

func (s *Store) bumpStats(fn func(*Stats)) {
	s.mu.Lock()
	fn(&s.stats)
	s.mu.Unlock()
}

func (s *Store) StatsSnapshot() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats
}

func (s *Store) CountRequest()    { s.bumpStats(func(st *Stats) { st.Requests++ }) }
func (s *Store) CountInjected()   { s.bumpStats(func(st *Stats) { st.Injected++ }) }
func (s *Store) CountNoProfile()  { s.bumpStats(func(st *Stats) { st.NoProfile++ }) }
func (s *Store) CountBadShape()   { s.bumpStats(func(st *Stats) { st.BadShape++ }) }
func (s *Store) CountAuthFail()   { s.bumpStats(func(st *Stats) { st.AuthFailures++ }) }
func (s *Store) CountRuleInject() { s.bumpStats(func(st *Stats) { st.RuleInject++ }) }
func (s *Store) CountRuleDeny()   { s.bumpStats(func(st *Stats) { st.RuleDeny++ }) }
func (s *Store) CountKeySkip()    { s.bumpStats(func(st *Stats) { st.KeySkips++ }) }
func (s *Store) CountUpstreamError() {
	s.bumpStats(func(st *Stats) { st.UpstreamErrors++ })
}

func (s *Store) Candidates(model string) []*Upstream {
	list := make([]Upstream, 0, 8)
	for _, u := range s.effectiveUpstreams() {
		if !u.Enabled {
			continue
		}
		if _, ok := resolveModel(&u, model); !ok {
			continue
		}
		list = append(list, u)
	}
	if len(list) == 0 {
		return nil
	}
	s.mu.Lock()
	s.rotation[model]++
	rot := s.rotation[model]
	s.mu.Unlock()

	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Priority != list[j].Priority {
			return list[i].Priority < list[j].Priority
		}
		return list[i].ID < list[j].ID
	})

	out := make([]*Upstream, 0, len(list))
	i := 0
	for i < len(list) {
		j := i
		for j < len(list) && list[j].Priority == list[i].Priority {
			j++
		}
		group := list[i:j]
		first := weightedStart(group, rot)
		cp := group[first]
		out = append(out, &cp)
		for k := 0; k < len(group); k++ {
			if k == first {
				continue
			}
			item := group[k]
			out = append(out, &item)
		}
		i = j
	}
	return out
}

type lease struct {
	up     *Upstream
	key    *keyState
	pool   *keyPool
	picked time.Time
}

func (l *lease) keyValue() string {
	if l == nil || l.key == nil {
		return ""
	}
	return l.key.key
}

func (l *lease) keyLabel() string {
	if l == nil || l.key == nil {
		return ""
	}
	return maskKey(l.key.key)
}

func (s *Store) Acquire(u *Upstream) (*lease, string) {
	st := s.stateFor(u.ID)
	cfg := s.Config()
	now := time.Now()

	st.mu.Lock()
	if !st.cooldownUntil.IsZero() && now.Before(st.cooldownUntil) {
		wait := time.Until(st.cooldownUntil).Round(time.Second)
		st.mu.Unlock()
		return nil, fmt.Sprintf("上游冷却中（%s 后恢复）", wait)
	}
	if !st.cooldownUntil.IsZero() {
		// 冷却到期 → 进入半开，只放行少量探测
		st.cooldownUntil = time.Time{}
		st.circuit = circuitHalfOpen
		st.halfInflight = 0
	}
	if st.circuit == circuitHalfOpen {
		limit := cfg.Retry.HalfOpenLimit
		if limit <= 0 {
			limit = 1
		}
		if st.halfInflight >= limit {
			st.mu.Unlock()
			return nil, "熔断半开探测中"
		}
		st.halfInflight++
	}
	if u.Concurrency > 0 && st.inflight >= u.Concurrency {
		st.mu.Unlock()
		return nil, "并发已满"
	}
	if u.RPM > 0 {
		if st.windowStart.IsZero() || now.Sub(st.windowStart) >= time.Minute {
			st.windowStart = now
			st.windowCount = 0
		}
		if st.windowCount >= u.RPM {
			st.mu.Unlock()
			return nil, "RPM 已达上限"
		}
		st.windowCount++
	}
	st.inflight++
	st.requests++
	st.mu.Unlock()

	pool := s.keyPoolFor(u)
	key, ok := pool.pick()
	if !ok {
		st.mu.Lock()
		if st.inflight > 0 {
			st.inflight--
		}
		st.mu.Unlock()
		s.CountKeySkip()
		return nil, "所有 key 都在冷却中"
	}
	_ = cfg
	return &lease{up: u, key: key, pool: pool, picked: time.Now()}, ""
}

func (s *Store) Release(l *lease) {
	if l == nil || l.up == nil {
		return
	}
	st := s.stateFor(l.up.ID)
	st.mu.Lock()
	if st.inflight > 0 {
		st.inflight--
	}
	st.mu.Unlock()
}

func (s *Store) RecordSuccess(l *lease, status int) {
	if l == nil || l.up == nil {
		return
	}
	st := s.stateFor(l.up.ID)
	latency := time.Since(l.picked)
	st.mu.Lock()
	if st.circuit == circuitHalfOpen {
		st.openCount = 0
		st.halfInflight--
		if st.halfInflight < 0 {
			st.halfInflight = 0
		}
	}
	st.circuit = circuitClosed
	st.fails = 0
	st.cooldownUntil = time.Time{}
	st.healthy = true
	st.lastStatus = status
	st.lastErr = ""
	st.lastSeen = time.Now()
	st.latency = latency
	st.successes++
	st.mu.Unlock()
	if l.pool != nil {
		l.pool.report(l.key, status, latency, "")
	}
}

func (s *Store) RecordFailure(l *lease, status int, msg string) {
	if l == nil || l.up == nil {
		s.CountUpstreamError()
		return
	}
	st := s.stateFor(l.up.ID)
	cfg := s.Config()
	latency := time.Since(l.picked)
	st.mu.Lock()
	st.fails++
	st.failures++
	st.lastStatus = status
	st.lastErr = msg
	st.lastSeen = time.Now()
	st.latency = latency
	if st.circuit == circuitHalfOpen {
		st.halfInflight--
		if st.halfInflight < 0 {
			st.halfInflight = 0
		}
	}
	if st.fails >= cfg.Retry.FailureThreshold || st.circuit == circuitHalfOpen {
		base := cfg.Retry.CooldownSeconds
		if base <= 0 {
			base = 30
		}
		backoff := base
		for i := 0; i < st.openCount && backoff < cfg.Retry.CooldownMaxSeconds; i++ {
			backoff *= 2
		}
		if backoff > cfg.Retry.CooldownMaxSeconds {
			backoff = cfg.Retry.CooldownMaxSeconds
		}
		st.openCount++
		st.circuit = circuitOpen
		st.cooldownUntil = time.Now().Add(time.Duration(backoff) * time.Second)
		st.healthy = false
	}
	st.mu.Unlock()
	if l.pool != nil {
		l.pool.report(l.key, status, latency, msg)
	}
	s.CountUpstreamError()
}

const (
	circuitClosed   = "closed"
	circuitOpen     = "open"
	circuitHalfOpen = "half_open"
)

// circuitOf 必须在持有 st.mu 时调用（避免再次加锁导致死锁）
func circuitOf(st *runtimeState) string {
	if st.circuit == "" {
		return circuitClosed
	}
	return st.circuit
}

func (s *Store) SetHealth(id string, healthy bool, models []string, errMsg string) {
	st := s.stateFor(id)
	st.mu.Lock()
	st.healthy = healthy
	st.models = append([]string(nil), models...)
	st.lastSeen = time.Now()
	if errMsg != "" {
		st.lastErr = errMsg
	}
	st.mu.Unlock()
}

func (s *Store) SnapshotUpstreams() []UpstreamState {
	ups := s.effectiveUpstreams()
	out := make([]UpstreamState, 0, len(ups))
	now := time.Now()
	for _, u := range ups {
		st := s.stateFor(u.ID)
		st.mu.Lock()
		cooldown := int64(0)
		if !st.cooldownUntil.IsZero() && now.Before(st.cooldownUntil) {
			cooldown = st.cooldownUntil.Unix()
		}
		item := UpstreamState{
			ID:            u.ID,
			Name:          u.Name,
			Enabled:       u.Enabled,
			Weight:        u.Weight,
			Healthy:       st.healthy,
			Fails:         st.fails,
			CooldownUntil: cooldown,
			Inflight:      st.inflight,
			WindowCount:   st.windowCount,
			Requests:      st.requests,
			Successes:     st.successes,
			Failures:      st.failures,
			LastError:     st.lastErr,
			LastStatus:    st.lastStatus,
			Circuit:       circuitOf(st),
			LatencyMS:     st.latency.Milliseconds(),
			Models:        append([]string(nil), st.models...),
		}
		if !st.lastSeen.IsZero() {
			item.LastSeen = st.lastSeen.Unix()
		}
		st.mu.Unlock()
		item.Keys = s.keyPoolFor(&u).snapshot()
		if u.Budget != nil {
			bv := s.BudgetSnapshot(u.ID, &u)
			item.Budget = &bv
		}
		out = append(out, item)
	}
	return out
}

func (s *Store) StatusPayload() map[string]any {
	cfg := s.Config()
	stats := s.StatsSnapshot()
	return map[string]any{
		"service":          "gateway-manager",
		"version":          version,
		"uptime":           time.Since(time.Unix(stats.StartedAt, 0)).Round(time.Second).String(),
		"bind":             cfg.Server.Bind,
		"port":             cfg.Server.Port,
		"endpoint":         fmt.Sprintf("http://%s:%d/v1", cfg.Server.Bind, cfg.Server.Port),
		"auth":             len(cfg.Server.AccessKeys) > 0,
		"stats":            stats,
		"upstreams":        s.SnapshotUpstreams(),
		"profiles":         cfg.Profiles,
		"rules":            cfg.Rules,
		"retry":            cfg.Retry,
		"key_strategy":     cfg.Server.KeyStrategy,
		"rule_stats":       s.RuleStatsSnapshot(),
		"persist":          s.persistInfo(),
		"experiments":      cfg.Experiments,
		"experiment_stats": s.ExperimentStats(),
	}
}

func (s *Store) SetStatePath(path string) {
	s.mu.Lock()
	s.statePath = path
	s.persistOn = path != ""
	if path == "" {
		s.persistOn = false
	}
	s.mu.Unlock()
}

func (s *Store) SetPersistEnabled(on bool) {
	s.mu.Lock()
	s.persistOn = on
	s.mu.Unlock()
}

func defaultStatePath(cfgPath string) string {
	if cfgPath == "" {
		return "gateway-manager.state.json"
	}
	base := filepath.Base(cfgPath)
	ext := filepath.Ext(base)
	base = strings.TrimSuffix(base, ext)
	dir := filepath.Dir(cfgPath)
	return filepath.Join(dir, base+".state.json")
}

func (s *Store) LoadState() error {
	s.mu.RLock()
	path := s.statePath
	s.mu.RUnlock()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	state := &persistedState{}
	if err := json.Unmarshal(raw, state); err != nil {
		return fmt.Errorf("状态文件解析失败: %w", err)
	}
	s.mu.Lock()
	s.persisted = state
	if state.SavedAt > 0 {
		s.lastSave = time.Unix(state.SavedAt, 0)
	}
	if state.RuleStats != nil {
		for id, hit := range state.RuleStats {
			if hit == nil {
				continue
			}
			cp := *hit
			s.ruleHits[id] = &cp
		}
	}
	// 已存在的 key 池立即套用
	for id, st := range s.rt {
		st.mu.Lock()
		if st.keys != nil && state.Upstreams != nil {
			st.keys.restore(state.Upstreams[id])
		}
		st.mu.Unlock()
	}
	s.mu.Unlock()
	s.logf("INFO", "已加载状态文件：%s", path)
	return nil
}

func (s *Store) ExportState() *persistedState {
	s.mu.RLock()
	path := s.statePath
	s.mu.RUnlock()
	if path == "" {
		return nil
	}
	cfg := s.Config()
	out := &persistedState{
		Version:   1,
		SavedAt:   time.Now().Unix(),
		Upstreams: map[string][]keyPersist{},
		RuleStats: map[string]*ruleHitState{},
	}
	for _, u := range cfg.Upstreams {
		if len(u.APIKeys) == 0 {
			continue
		}
		pool := s.keyPoolFor(&u)
		out.Upstreams[u.ID] = pool.export()
	}
	s.mu.RLock()
	for id, hit := range s.ruleHits {
		if hit == nil {
			continue
		}
		cp := *hit
		out.RuleStats[id] = &cp
	}
	s.mu.RUnlock()
	return out
}

func (s *Store) SaveState() error {
	s.mu.RLock()
	path := s.statePath
	s.mu.RUnlock()
	if path == "" {
		return nil
	}
	state := s.ExportState()
	if state == nil {
		return nil
	}
	out, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	s.mu.Lock()
	s.dirty = false
	s.lastSave = time.Now()
	s.mu.Unlock()
	return nil
}

func (s *Store) MarkStateDirty() {
	s.mu.Lock()
	s.dirty = true
	s.mu.Unlock()
}

func (s *Store) RunPersistLoop(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 20 * time.Second
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := s.SaveState(); err != nil {
				s.logf("ERR", "退出前保存状态失败：%v", err)
			}
			return
		case <-ticker.C:
			s.mu.RLock()
			dirty := s.dirty
			on := s.persistOn
			s.mu.RUnlock()
			if !dirty || !on {
				continue
			}
			if err := s.SaveState(); err != nil {
				s.logf("WARN", "保存状态失败：%v", err)
			}
		}
	}
}

func (s *Store) RecordRuleHits(matched []string, skipped []string, denied bool) {
	now := time.Now().Unix()
	s.mu.Lock()
	for _, id := range matched {
		hit := s.ruleHits[id]
		if hit == nil {
			hit = &ruleHitState{}
			s.ruleHits[id] = hit
		}
		hit.Matched++
		hit.LastHit = now
		if denied {
			hit.Denied++
		}
	}
	for _, id := range skipped {
		hit := s.ruleHits[id]
		if hit == nil {
			hit = &ruleHitState{}
			s.ruleHits[id] = hit
		}
		hit.Skipped++
	}
	s.dirty = true
	s.mu.Unlock()
}

func (s *Store) RuleStatsSnapshot() map[string]ruleHitState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]ruleHitState, len(s.ruleHits))
	for id, hit := range s.ruleHits {
		if hit == nil {
			continue
		}
		out[id] = *hit
	}
	return out
}

func (s *Store) ResetRuleStats() {
	s.mu.Lock()
	s.ruleHits = map[string]*ruleHitState{}
	s.dirty = true
	s.mu.Unlock()
}

func (s *Store) persistInfo() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	last := int64(0)
	if !s.lastSave.IsZero() {
		last = s.lastSave.Unix()
	}
	return map[string]any{
		"enabled":   s.persistOn,
		"path":      s.statePath,
		"last_save": last,
		"dirty":     s.dirty,
	}
}

func (s *Store) ObserveRequest(kind string, status int, latency time.Duration) {
	s.mu.Lock()
	if s.reqHist == nil {
		s.reqHist = newHistogram()
	}
	if s.kindHits == nil {
		s.kindHits = map[string]uint64{}
	}
	hist := s.reqHist
	s.kindHits[kind]++
	s.mu.Unlock()
	hist.observe(latency.Seconds())
	_ = status
}

func (s *Store) ObserveUpstream(id string, status int, latency time.Duration) {
	s.mu.Lock()
	if s.upHist == nil {
		s.upHist = map[string]*histogram{}
	}
	if s.respCodes == nil {
		s.respCodes = map[string]map[string]uint64{}
	}
	hist := s.upHist[id]
	if hist == nil {
		hist = newHistogram()
		s.upHist[id] = hist
	}
	codes := s.respCodes[id]
	if codes == nil {
		codes = map[string]uint64{}
		s.respCodes[id] = codes
	}
	code := "error"
	if status > 0 {
		code = formatUint(uint64(status))
	}
	codes[code]++
	s.mu.Unlock()
	hist.observe(latency.Seconds())
}

func (s *Store) RequestHistogram() histogramSnapshot {
	s.mu.RLock()
	hist := s.reqHist
	s.mu.RUnlock()
	if hist == nil {
		return histogramSnapshot{}
	}
	return hist.snapshot()
}

func (s *Store) UpstreamHistograms() map[string]histogramSnapshot {
	s.mu.RLock()
	src := make(map[string]*histogram, len(s.upHist))
	for k, v := range s.upHist {
		src[k] = v
	}
	s.mu.RUnlock()
	out := make(map[string]histogramSnapshot, len(src))
	for k, v := range src {
		out[k] = v.snapshot()
	}
	return out
}

func (s *Store) ResponseCodes() map[string]map[string]uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]map[string]uint64, len(s.respCodes))
	for up, codes := range s.respCodes {
		cp := make(map[string]uint64, len(codes))
		for code, n := range codes {
			cp[code] = n
		}
		out[up] = cp
	}
	return out
}

func (s *Store) KindHits() map[string]uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]uint64, len(s.kindHits))
	for k, v := range s.kindHits {
		out[k] = v
	}
	return out
}

func (s *Store) AddReplay(entry ReplayEntry) string {
	s.mu.Lock()
	limit := s.cfg.Server.ReplayLimit
	if limit <= 0 {
		s.mu.Unlock()
		return ""
	}
	s.replaySeq++
	entry.ID = "rp-" + formatUint(s.replaySeq)
	if entry.Time == 0 {
		entry.Time = time.Now().Unix()
	}
	s.replay = append(s.replay, entry)
	if len(s.replay) > limit {
		s.replay = append([]ReplayEntry(nil), s.replay[len(s.replay)-limit:]...)
	}
	s.mu.Unlock()
	return entry.ID
}

func (s *Store) ReplayEntries() []ReplayEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ReplayEntry, len(s.replay))
	copy(out, s.replay)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (s *Store) FindReplay(id string) (ReplayEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.replay {
		if entry.ID == id {
			return entry, true
		}
	}
	return ReplayEntry{}, false
}

func (s *Store) ClearReplay() {
	s.mu.Lock()
	s.replay = nil
	s.mu.Unlock()
}

func (s *Store) ReplayCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.replay)
}

func weightedStart(group []Upstream, rot uint64) int {
	total := 0
	for _, u := range group {
		if u.Weight > 0 {
			total += u.Weight
		}
	}
	if total <= 0 {
		return int(rot % uint64(len(group)))
	}
	x := int(rot % uint64(total))
	acc := 0
	fallback := -1
	for i, u := range group {
		if fallback < 0 {
			fallback = i
		}
		if u.Weight <= 0 {
			continue
		}
		acc += u.Weight
		if x < acc {
			return i
		}
	}
	if fallback < 0 {
		return 0
	}
	return fallback
}

func (s *Store) ObserveRoute(model string, upstreamID string) {
	s.mu.Lock()
	if s.routeHits == nil {
		s.routeHits = map[string]map[string]uint64{}
	}
	per := s.routeHits[model]
	if per == nil {
		per = map[string]uint64{}
		s.routeHits[model] = per
	}
	per[upstreamID]++
	s.mu.Unlock()
}

func (s *Store) RouteHits() map[string]map[string]uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]map[string]uint64, len(s.routeHits))
	for model, per := range s.routeHits {
		cp := make(map[string]uint64, len(per))
		for id, n := range per {
			cp[id] = n
		}
		out[model] = cp
	}
	return out
}

func (s *Store) ObserveTiming(queue time.Duration, ttfb time.Duration, relay time.Duration) {
	s.mu.Lock()
	if s.queueHist == nil {
		s.queueHist = newHistogram()
	}
	if s.ttfbHist == nil {
		s.ttfbHist = newHistogram()
	}
	if s.relayHist == nil {
		s.relayHist = newHistogram()
	}
	q, t, r := s.queueHist, s.ttfbHist, s.relayHist
	s.mu.Unlock()
	if q != nil {
		q.observe(queue.Seconds())
	}
	if t != nil {
		t.observe(ttfb.Seconds())
	}
	if r != nil {
		r.observe(relay.Seconds())
	}
}

func (s *Store) TimingHistograms() (histogramSnapshot, histogramSnapshot, histogramSnapshot) {
	s.mu.RLock()
	q, t, r := s.queueHist, s.ttfbHist, s.relayHist
	s.mu.RUnlock()
	var qs, ts, rs histogramSnapshot
	if q != nil {
		qs = q.snapshot()
	}
	if t != nil {
		ts = t.snapshot()
	}
	if r != nil {
		rs = r.snapshot()
	}
	return qs, ts, rs
}

func (s *Store) ShouldSample() bool {
	s.mu.Lock()
	every := 0
	if s.cfg != nil {
		every = s.cfg.Server.SampleEvery
	}
	if every <= 0 {
		s.mu.Unlock()
		return false
	}
	s.sampleTick++
	hit := s.sampleTick%uint64(every) == 0
	s.mu.Unlock()
	return hit
}

func (s *Store) AddSample(entry ReplayEntry) string {
	s.mu.Lock()
	limit := s.cfg.Server.SampleLimit
	if limit <= 0 {
		s.mu.Unlock()
		return ""
	}
	s.sampleSeq++
	entry.ID = `sp-` + formatUint(s.sampleSeq)
	if entry.Time == 0 {
		entry.Time = time.Now().Unix()
	}
	s.samples = append(s.samples, entry)
	if len(s.samples) > limit {
		s.samples = append([]ReplayEntry(nil), s.samples[len(s.samples)-limit:]...)
	}
	s.mu.Unlock()
	return entry.ID
}

func (s *Store) Samples() []ReplayEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ReplayEntry, len(s.samples))
	copy(out, s.samples)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (s *Store) FindSample(id string) (ReplayEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.samples {
		if entry.ID == id {
			return entry, true
		}
	}
	return ReplayEntry{}, false
}

func (s *Store) ClearSamples() {
	s.mu.Lock()
	s.samples = nil
	s.mu.Unlock()
}

func (s *Store) SampleCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.samples)
}

func todayKey() string {
	return time.Now().Format("2006-01-02")
}

func (s *Store) budgetFor(id string) *budgetState {
	day := todayKey()
	st := s.budget[id]
	if st == nil {
		st = &budgetState{Day: day}
		s.budget[id] = st
	}
	if st.Day != day {
		st.Day = day
		st.Reqs = 0
		st.Tokens = 0
	}
	return st
}

func (s *Store) CheckBudget(u *Upstream, estTokens int) (bool, string) {
	if u.Budget == nil {
		return true, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.budget == nil {
		s.budget = map[string]*budgetState{}
	}
	st := s.budgetFor(u.ID)
	if u.Budget.MaxRequestsPerDay > 0 && st.Reqs >= uint64(u.Budget.MaxRequestsPerDay) {
		return false, "日请求预算已用尽"
	}
	if u.Budget.MaxTokensPerDay > 0 && st.Tokens+uint64(estTokens) > uint64(u.Budget.MaxTokensPerDay) {
		return false, "日 token 预算已用尽"
	}
	return true, ""
}

func (s *Store) ConsumeBudget(u *Upstream, tokens int) {
	if u.Budget == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.budget == nil {
		s.budget = map[string]*budgetState{}
	}
	st := s.budgetFor(u.ID)
	st.Reqs++
	if tokens > 0 {
		st.Tokens += uint64(tokens)
	}
}

func (s *Store) BudgetSnapshot(id string, u *Upstream) BudgetView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	view := BudgetView{Day: todayKey()}
	if u != nil && u.Budget != nil {
		view.MaxRequests = u.Budget.MaxRequestsPerDay
		view.MaxTokens = u.Budget.MaxTokensPerDay
	}
	if s.budget == nil {
		return view
	}
	st := s.budget[id]
	if st == nil || st.Day != view.Day {
		return view
	}
	view.Requests = st.Reqs
	view.Tokens = st.Tokens
	if view.MaxRequests > 0 && st.Reqs >= uint64(view.MaxRequests) {
		view.Exhausted = true
	}
	if view.MaxTokens > 0 && st.Tokens >= uint64(view.MaxTokens) {
		view.Exhausted = true
	}
	return view
}

func estimateTokens(payload []byte) int {
	if len(payload) == 0 {
		return 0
	}
	est := len(payload) / 4
	if est < 1 {
		est = 1
	}
	return est
}

func (s *Store) channelByID(id string) (Channel, ChannelPreset, bool) {
	cfg := s.Config()
	for _, ch := range cfg.Channels {
		if ch.ID != id {
			continue
		}
		preset, ok := findPreset(ch.Preset)
		return ch, preset, ok
	}
	return Channel{}, ChannelPreset{}, false
}

func (s *Store) AddTokenUsage(id string, tokens int) {
	if tokens <= 0 {
		return
	}
	s.mu.Lock()
	if s.tokUsage == nil {
		s.tokUsage = map[string]uint64{}
	}
	s.tokUsage[id] += uint64(tokens)
	s.mu.Unlock()
}

func (s *Store) TokenUsage() map[string]uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]uint64, len(s.tokUsage))
	for k, v := range s.tokUsage {
		out[k] = v
	}
	return out
}

func (s *Store) ReconcileBudget(u *Upstream, estimate int, actual int) {
	if u == nil || u.Budget == nil || actual <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.budget == nil {
		s.budget = map[string]*budgetState{}
	}
	st := s.budgetFor(u.ID)
	if st.Tokens >= uint64(estimate) {
		st.Tokens -= uint64(estimate)
	} else {
		st.Tokens = 0
	}
	st.Tokens += uint64(actual)
}

// effectiveUpstreams = 配置里的上游 + 联邦 peer（当 peer 被声明为上游时）
func (s *Store) effectiveUpstreams() []Upstream {
	cfg := s.Config()
	out := make([]Upstream, 0, len(cfg.Upstreams)+len(cfg.Federation.Peers)+len(cfg.Channels))
	out = append(out, cfg.Upstreams...)
	for _, ch := range cfg.Channels {
		if !ch.Enabled {
			continue
		}
		preset, ok := findPreset(ch.Preset)
		if !ok {
			continue
		}
		out = append(out, ch.toUpstream(preset))
	}
	if !cfg.Federation.Enabled {
		return out
	}
	for _, peer := range cfg.Federation.Peers {
		if !peer.AsUpstream {
			continue
		}
		name := peer.Name
		if name == "" {
			name = peer.ID
		}
		keys := []string{}
		if peer.AccessKey != "" {
			keys = append(keys, peer.AccessKey)
		}
		out = append(out, Upstream{
			ID:       "peer-" + peer.ID,
			Name:     "peer:" + name,
			BaseURL:  peer.BaseURL,
			APIKeys:  keys,
			Models:   append([]string(nil), peer.Models...),
			Priority: peer.Priority,
			Weight:   peer.Weight,
			Enabled:  true,
		})
	}
	return out
}

func (s *Store) ShouldMirror() bool {
	cfg := s.Config()
	if !cfg.Server.MirrorEnabled || cfg.Server.MirrorRatio <= 0 || cfg.Server.MirrorUpstream == "" {
		return false
	}
	s.mu.Lock()
	s.mirrorTick++
	hit := int(s.mirrorTick%100) < cfg.Server.MirrorRatio
	s.mu.Unlock()
	return hit
}

func (s *Store) RecordMirror(id string, ok bool) {
	s.mu.Lock()
	if s.mirrorOK == nil {
		s.mirrorOK = map[string]uint64{}
		s.mirrorFail = map[string]uint64{}
	}
	if ok {
		s.mirrorOK[id]++
	} else {
		s.mirrorFail[id]++
	}
	s.mu.Unlock()
}

func (s *Store) MirrorStats() (map[string]uint64, map[string]uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ok := make(map[string]uint64, len(s.mirrorOK))
	fail := make(map[string]uint64, len(s.mirrorFail))
	for k, v := range s.mirrorOK {
		ok[k] = v
	}
	for k, v := range s.mirrorFail {
		fail[k] = v
	}
	return ok, fail
}

type expArmStats struct {
	Count     uint64 `json:"count"`
	Errors    uint64 `json:"errors"`
	LatencyMS int64  `json:"latency_ms_sum"`
	Tokens    int64  `json:"tokens_sum"`
}

func (s *Store) PickExperiment(model string) (*Experiment, *ExperimentArm) {
	cfg := s.Config()
	if len(cfg.Experiments) == 0 {
		return nil, nil
	}
	for i := range cfg.Experiments {
		exp := &cfg.Experiments[i]
		if len(exp.Models) > 0 {
			matched := false
			for _, pattern := range exp.Models {
				if matchModelPattern(pattern, model) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		total := 0
		for _, arm := range exp.Arms {
			if arm.Weight > 0 {
				total += arm.Weight
			}
		}
		if total <= 0 {
			return exp, &exp.Arms[0]
		}
		s.mu.Lock()
		s.expTick++
		tick := s.expTick
		s.mu.Unlock()
		x := int(tick % uint64(total))
		acc := 0
		for j := range exp.Arms {
			arm := &exp.Arms[j]
			if arm.Weight <= 0 {
				continue
			}
			acc += arm.Weight
			if x < acc {
				return exp, arm
			}
		}
		return exp, &exp.Arms[0]
	}
	return nil, nil
}

func (s *Store) RecordExperiment(expID string, armID string, latency time.Duration, tokens int, ok bool) {
	if expID == "" || armID == "" {
		return
	}
	s.mu.Lock()
	if s.expStats == nil {
		s.expStats = map[string]map[string]*expArmStats{}
	}
	per := s.expStats[expID]
	if per == nil {
		per = map[string]*expArmStats{}
		s.expStats[expID] = per
	}
	st := per[armID]
	if st == nil {
		st = &expArmStats{}
		per[armID] = st
	}
	st.Count++
	if !ok {
		st.Errors++
	}
	st.LatencyMS += latency.Milliseconds()
	if tokens > 0 {
		st.Tokens += int64(tokens)
	}
	s.mu.Unlock()
}

func (s *Store) ExperimentStats() map[string]map[string]expArmStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]map[string]expArmStats, len(s.expStats))
	for expID, per := range s.expStats {
		cp := make(map[string]expArmStats, len(per))
		for armID, st := range per {
			if st == nil {
				continue
			}
			cp[armID] = *st
		}
		out[expID] = cp
	}
	return out
}
