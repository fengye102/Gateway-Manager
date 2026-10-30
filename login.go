package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------- 极简 WebSocket 客户端（仅文本帧，够 CDP 用） ----------

type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
	mu   sync.Mutex
}

func dialWS(rawURL string) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return nil, err
	}
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := u.Path
	if path == "" {
		path = "/"
	}
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, u.Host, key)
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !strings.Contains(statusLine, "101") {
		conn.Close()
		return nil, fmt.Errorf("WS 握手失败：%s", strings.TrimSpace(statusLine))
	}
	headers := map[string]string{}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if idx := strings.Index(line, ":"); idx > 0 {
			headers[strings.ToLower(strings.TrimSpace(line[:idx]))] = strings.TrimSpace(line[idx+1:])
		}
	}
	if accept := headers["sec-websocket-accept"]; accept != "" {
		h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		if accept != base64.StdEncoding.EncodeToString(h[:]) {
			conn.Close()
			return nil, fmt.Errorf("WS 握手校验失败")
		}
	}
	return &wsConn{conn: conn, br: br}, nil
}

func (w *wsConn) writeFrame(opcode byte, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	header := []byte{0x80 | opcode}
	n := len(payload)
	switch {
	case n < 126:
		header = append(header, byte(n)|0x80)
	case n <= 65535:
		header = append(header, 126|0x80, byte(n>>8), byte(n))
	default:
		header = append(header, 127|0x80)
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], uint64(n))
		header = append(header, buf[:]...)
	}
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	header = append(header, mask...)
	masked := make([]byte, n)
	for i := 0; i < n; i++ {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := w.conn.Write(append(header, masked...)); err != nil {
		return err
	}
	return nil
}

func (w *wsConn) readFrame() (byte, []byte, error) {
	head := make([]byte, 2)
	if _, err := io.ReadFull(w.br, head); err != nil {
		return 0, nil, err
	}
	opcode := head[0] & 0x0f
	masked := head[1]&0x80 != 0
	n := uint64(head[1] & 0x7f)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(w.br, ext[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(w.br, ext[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(ext[:])
	}
	var mask []byte
	if masked {
		mask = make([]byte, 4)
		if _, err := io.ReadFull(w.br, mask); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(w.br, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}

func (w *wsConn) writeText(text []byte) error { return w.writeFrame(0x1, text) }

func (w *wsConn) readMessage() ([]byte, error) {
	var buf []byte
	for {
		opcode, payload, err := w.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case 0x1, 0x0:
			buf = append(buf, payload...)
			if opcode == 0x1 {
				return buf, nil
			}
		case 0x8:
			return nil, io.EOF
		case 0x9:
			_ = w.writeFrame(0xA, payload)
		}
	}
}

func (w *wsConn) close() {
	if w.conn != nil {
		_ = w.writeFrame(0x8, nil)
		_ = w.conn.Close()
	}
}

// ---------- CDP ----------

type cdpClient struct {
	ws  *wsConn
	seq int
	mu  sync.Mutex
}

func (c *cdpClient) call(method string, params any, out any, timeout time.Duration) error {
	return c.callSession("", method, params, out, timeout)
}

func (c *cdpClient) callSession(sessionID string, method string, params any, out any, timeout time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	id := c.seq
	payload := map[string]any{"id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	if sessionID != "" {
		payload["sessionId"] = sessionID
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := c.ws.writeText(raw); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = c.ws.conn.SetReadDeadline(deadline)
		msg, err := c.ws.readMessage()
		if err != nil {
			return err
		}
		var resp struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(msg, &resp); err != nil {
			continue
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return fmt.Errorf("%s", resp.Error.Message)
		}
		if out != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	}
	return fmt.Errorf("CDP 调用超时：%s", method)
}

type cdpCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
}

// ---------- 浏览器登录会话 ----------

type loginSession struct {
	ID        string    `json:"id"`
	Preset    string    `json:"preset"`
	LoginURL  string    `json:"login_url"`
	Browser   string    `json:"browser"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"started_at"`
	closed    bool
	client    *cdpClient
	proc      *exec.Cmd
	mu        sync.Mutex
}

var findBrowserFunc = findChromiumBrowser

func findChromiumBrowser() (string, bool) {
	candidates := []string{
		"C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
		"C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
		"C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
		"C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe",
	}
	envOr := func(key string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		switch key {
		case "LOCALAPPDATA":
			if home := os.Getenv("USERPROFILE"); home != "" {
				return filepath.Join(home, "AppData", "Local")
			}
		case "ProgramFiles":
			return "C:\\Program Files"
		case "ProgramFiles(x86)":
			return "C:\\Program Files (x86)"
		}
		return ""
	}
	for _, dir := range []string{
		filepath.Join(envOr("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(envOr("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(envOr("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(envOr("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(envOr("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
	} {
		if dir != "" {
			candidates = append(candidates, dir)
		}
	}
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		)
	case "linux":
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge"} {
			if p, err := exec.LookPath(name); err == nil {
				return p, true
			}
		}
	}
	for _, name := range []string{"chrome", "chrome.exe", "msedge", "msedge.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, true
		}
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c, true
		}
	}
	return "", false
}

func newLoginSession(id string, preset ChannelPreset, profileRoot string) (*loginSession, error) {
	browser, ok := findBrowserFunc()
	if !ok {
		return nil, fmt.Errorf("未找到 Chrome / Edge / Chromium，无法网页登录（可改用手动粘贴凭证）")
	}
	if strings.TrimSpace(preset.LoginURL) == "" {
		return nil, fmt.Errorf("渠道 %s 没有配置登录页地址", preset.ID)
	}
	if err := os.MkdirAll(profileRoot, 0o755); err != nil {
		return nil, err
	}

	// 1) 若该 profile 已经有可用的调试实例（浏览器还开着），直接复用
	if session, err := attachExisting(profileRoot, id, preset); err == nil && session != nil {
		return session, nil
	}

	// 2) 正常启动；失败时换一个全新 profile 再试一次
	attempts := []string{
		filepath.Join(profileRoot, "login-"+id),
		filepath.Join(profileRoot, "login-"+id+"-"+strconv.FormatInt(time.Now().UnixNano(), 36)),
	}
	var lastErr error
	for i, profileDir := range attempts {
		session, err := launchLoginBrowser(browser, profileDir, id, preset)
		if err == nil {
			return session, nil
		}
		lastErr = err
		if i == 0 {
			logfQuiet("复用 profile 启动失败，改用全新 profile 重试：%v", err)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("浏览器启动失败")
	}
	return nil, lastErr
}

// attachExisting：profile 里已有活着的调试端口就直接接上（浏览器当前开着）
func attachExisting(profileRoot, id string, preset ChannelPreset) (*loginSession, error) {
	portFile := filepath.Join(profileRoot, "login-"+id, "DevToolsActivePort")
	raw, err := os.ReadFile(portFile)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("DevToolsActivePort 内容不完整")
	}
	port, err := strconvAtoi(strings.TrimSpace(lines[0]))
	if err != nil || port <= 0 {
		return nil, fmt.Errorf("DevToolsActivePort 端口无效")
	}
	wsPath := strings.TrimSpace(lines[1])
	ws, err := dialWS(fmt.Sprintf("ws://127.0.0.1:%d%s", port, wsPath))
	if err != nil {
		return nil, err
	}
	return &loginSession{
		ID: id, Preset: preset.ID, LoginURL: preset.LoginURL, Browser: "已运行的浏览器进程",
		Port: port, StartedAt: time.Now(), client: &cdpClient{ws: ws},
	}, nil
}

// launchLoginBrowser：用独立 profile 启动浏览器，并等待新的调试端口
func launchLoginBrowser(browser, profileDir, id string, preset ChannelPreset) (*loginSession, error) {
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return nil, err
	}
	portFile := filepath.Join(profileDir, "DevToolsActivePort")
	// 关键：先删掉上一次残留的端口文件，否则会读到过期端口
	_ = os.Remove(portFile)
	for _, lock := range []string{"SingletonLock", "SingletonSocket", "lockfile"} {
		_ = os.Remove(filepath.Join(profileDir, lock))
	}

	args := []string{
		"--remote-debugging-port=0",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-popup-blocking",
		"--disable-features=Translate",
		"--new-window",
		preset.LoginURL,
	}
	cmd := exec.Command(browser, args...)
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动浏览器失败：%v", err)
	}

	var port int
	var wsPath string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(120 * time.Millisecond)
		raw, err := os.ReadFile(portFile)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if len(lines) < 2 {
			continue
		}
		p, err := strconvAtoi(strings.TrimSpace(lines[0]))
		if err != nil || p <= 0 {
			continue
		}
		port = p
		wsPath = strings.TrimSpace(lines[1])
		break
	}
	if port == 0 {
		detail := ""
		if stderr != nil {
			buf := make([]byte, 2048)
			_ = cmd.Process.Kill()
			n, _ := stderr.Read(buf)
			detail = strings.TrimSpace(string(buf[:n]))
		}
		_ = cmd.Process.Kill()
		msg := fmt.Sprintf("浏览器没有打开调试端口（20 秒超时，profile=%s）", profileDir)
		if detail != "" {
			msg += "；浏览器输出：" + truncateForReport(detail, 200)
		}
		return nil, fmt.Errorf("%s", msg)
	}
	ws, err := dialWS(fmt.Sprintf("ws://127.0.0.1:%d%s", port, wsPath))
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("连接调试端口 %d 失败：%v", port, err)
	}
	return &loginSession{
		ID: id, Preset: preset.ID, LoginURL: preset.LoginURL, Browser: browser,
		Port: port, StartedAt: time.Now(), client: &cdpClient{ws: ws}, proc: cmd,
	}, nil
}

// logfQuiet：登录流程内部日志，写 stderr 即可
func logfQuiet(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[login] "+format+"\n", args...)
}

func strconvAtoi(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

// cookiesForPreset 读取该渠道域名下的 cookie
func (s *loginSession) cookiesForPreset(preset ChannelPreset) ([]cdpCookie, error) {
	if s.closed || s.client == nil {
		return nil, fmt.Errorf("登录会话已结束")
	}
	var result struct {
		Cookies []cdpCookie `json:"cookies"`
	}
	if err := s.client.call("Storage.getCookies", map[string]any{}, &result, 8*time.Second); err != nil {
		return nil, err
	}
	host := hostOf(preset.BaseURL)
	out := make([]cdpCookie, 0, len(result.Cookies))
	for _, c := range result.Cookies {
		domain := strings.TrimPrefix(strings.ToLower(c.Domain), ".")
		if host == "" || strings.HasSuffix(host, domain) || strings.HasSuffix(domain, host) {
			out = append(out, c)
		}
	}
	return out, nil
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// storageSnapshot 读取页面 localStorage + sessionStorage（DeepSeek/Trae 这类把 token 放在这里）
func (s *loginSession) storageSnapshot(preset ChannelPreset) (map[string]string, error) {
	if s.closed || s.client == nil {
		return nil, fmt.Errorf("登录会话已结束")
	}
	host := hostOf(preset.BaseURL)
	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
		} `json:"targetInfos"`
	}
	if err := s.client.call("Target.getTargets", nil, &targets, 5*time.Second); err != nil {
		return nil, err
	}
	targetID := ""
	for _, t := range targets.TargetInfos {
		if t.Type != "page" {
			continue
		}
		if host == "" || strings.Contains(t.URL, host) {
			targetID = t.TargetID
			break
		}
	}
	if targetID == "" {
		return map[string]string{}, nil
	}
	var attach struct {
		SessionID string `json:"sessionId"`
	}
	if err := s.client.call("Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true}, &attach, 5*time.Second); err != nil {
		return nil, err
	}
	defer func() {
		_ = s.client.call("Target.detachFromTarget", map[string]any{"sessionId": attach.SessionID}, nil, 3*time.Second)
	}()
	var eval struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	expr := "(function(){var o={};try{for(var i=0;i<localStorage.length;i++){var k=localStorage.key(i);o['localStorage.'+k]=localStorage.getItem(k);}}catch(e){}" +
		"try{for(var j=0;j<sessionStorage.length;j++){var k2=sessionStorage.key(j);o['sessionStorage.'+k2]=sessionStorage.getItem(k2);}}catch(e){}" +
		"return JSON.stringify(o);})()"
	if err := s.client.callSession(attach.SessionID, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true}, &eval, 8*time.Second); err != nil {
		return nil, err
	}
	out := map[string]string{}
	if strings.TrimSpace(eval.Result.Value) == "" {
		return out, nil
	}
	_ = json.Unmarshal([]byte(eval.Result.Value), &out)
	return out, nil
}

// pickTokenFromStorage 按关键词挑一个最像 token 的键
func pickTokenFromStorage(storage map[string]string) (string, string) {
	bestKey, bestVal := "", ""
	bestScore := -1
	for k, v := range storage {
		if strings.TrimSpace(v) == "" || len(v) < 8 {
			continue
		}
		lower := strings.ToLower(k)
		score := 0
		switch {
		case strings.Contains(lower, "token"):
			score = 3
		case strings.Contains(lower, "auth"):
			score = 2
		case strings.Contains(lower, "session"):
			score = 2
		case strings.Contains(lower, "uid"), strings.Contains(lower, "user"):
			score = 1
		}
		if score > bestScore {
			bestScore, bestKey, bestVal = score, k, v
		}
	}
	return bestKey, bestVal
}

// unwrapStorageValue：页面存储经常是 {"value":"...","__version":0} 结构
func unwrapStorageValue(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		return trimmed
	}
	var wrapper struct {
		Value any `json:"value"`
	}
	if err := json.Unmarshal([]byte(trimmed), &wrapper); err != nil {
		return trimmed
	}
	if s, ok := wrapper.Value.(string); ok {
		return s
	}
	return trimmed
}

func cookiesToToken(cookies []cdpCookie) string {
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

func (s *loginSession) closeSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.client != nil {
		s.client.ws.close()
	}
	if s.proc != nil && s.proc.Process != nil {
		_ = s.proc.Process.Kill()
	}
}
