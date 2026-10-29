package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var version = "2.5.0"

const usageText = `
网关管家 gateway-manager —— 本机网关聚合 + 策略注入（Windows / Linux 通用）

用法：
  gateway-manager serve     [-config config.json] [-listen 127.0.0.1:7878]
  gateway-manager discover  [-range 3000-9999] [-priority] [-workers 96] [-json]
  gateway-manager check     [-config config.json]
  gateway-manager init      [-config config.json] [-force]
  gateway-manager dump      [-config config.json]
  gateway-manager version

子命令说明：
  serve     启动本地网关（OpenAI / Anthropic 兼容，带策略注入）
  discover  扫描本机 127.0.0.1 上的 OpenAI 兼容网关
  check     对配置文件里的上游做一次健康检查
  init      生成示例配置
  dump      打印配置（默认值归一化后的结果）
`

func usage() {
	fmt.Print(usageText)
}

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "probe-deepseek":
		runProbeDeepSeek(args)
	case "serve", "run", "start":
		runServe(args, len(os.Args) == 1)
	case "discover", "scan":
		runDiscover(args)
	case "check":
		runCheck(args)
	case "init":
		runInit(args)
	case "dump":
		runDump(args)
	case "version", "-v", "--version":
		fmt.Println("gateway-manager " + version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令：%s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func runProbeDeepSeek(args []string) {
	fs := flag.NewFlagSet("probe-deepseek", flag.ExitOnError)
	credsPath := fs.String("creds", "creds.json", "含 token / cookie 的 JSON 文件")
	prompt := fs.String("prompt", "你好，用一句话介绍你自己", "发送内容")
	thinking := fs.Bool("thinking", false, "开启思考")
	search := fs.Bool("search", false, "开启联网")
	headBytes := fs.Int("head", 1500, "打印响应前多少字节")
	powDebug := fs.Bool("powdebug", false, "只打印 PoW 挑战与字节序匹配结果")
	_ = fs.Parse(args)

	var creds struct {
		Token  string `json:"token"`
		Cookie string `json:"cookie"`
	}
	raw, err := os.ReadFile(*credsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取凭证失败：%v\n", err)
		os.Exit(1)
	}
	if err := json.Unmarshal(raw, &creds); err != nil {
		fmt.Fprintf(os.Stderr, "解析凭证失败：%v\n", err)
		os.Exit(1)
	}

	client := newDeepseekClient(creds.Token, creds.Cookie, 180*time.Second)
	ctx := context.Background()
	if *powDebug {
		out, status, err := client.postJSON(ctx, "/api/v0/chat/create_pow_challenge", map[string]any{"target_path": "/api/v0/chat/completion"}, nil)
		fmt.Printf("challenge HTTP %d: %s\n", status, string(out))
		if err == nil {
			var payload struct {
				Data struct {
					BizData struct {
						Challenge deepseekChallenge `json:"challenge"`
					} `json:"biz_data"`
				} `json:"data"`
			}
			if json.Unmarshal(out, &payload) == nil {
				chal := payload.Data.BizData.Challenge
				fmt.Printf("algorithm=%s difficulty=%d expire_at=%d salt=%s\n", chal.Algorithm, chal.Difficulty, chal.ExpireAt, chal.Salt)
				limit := chal.Difficulty
				if limit <= 0 {
					limit = 144000
				}
				fmt.Println("变体结果:", solveDebug(chal, limit))
			}
		}
		return
	}

	sessionID, err := client.createSession(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建会话失败：%v\n", err)
		os.Exit(1)
	}
	fmt.Println("会话:", sessionID)
	promptText := "<｜User｜>" + *prompt + "<｜Assistant｜>"
	started := time.Now()
	resp, err := client.complete(ctx, sessionID, promptText, *thinking, *search)
	if err != nil {
		fmt.Fprintf(os.Stderr, "请求失败：%v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	fmt.Printf("HTTP %d · %d 字节 · %dms · ct=%s\n", resp.StatusCode, len(body), time.Since(started).Milliseconds(), resp.Header.Get("Content-Type"))
	n := *headBytes
	if n > len(body) {
		n = len(body)
	}
	fmt.Println("---- 原始响应 ----")
	fmt.Println(string(body[:n]))
}

func runServe(args []string, bare bool) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "config.json", "配置文件路径")
	listen := fs.String("listen", "", "覆盖监听地址，例如 127.0.0.1:7878")
	statePath := fs.String("state", "", "运行状态文件路径（key 健康分 / 规则命中），默认与配置同目录")
	openUI := fs.Bool("open", false, "启动后自动用浏览器打开控制台")
	noBrowser := fs.Bool("no-browser", false, "不自动打开浏览器（双击启动时默认会打开）")
	_ = fs.Parse(args)

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("未找到配置 %s，正在生成默认配置...\n", *cfgPath)
			if saveErr := SaveConfig(*cfgPath, SampleConfig()); saveErr != nil {
				fmt.Fprintf(os.Stderr, "生成默认配置失败：%v\n", saveErr)
				pauseIfInteractive()
				os.Exit(1)
			}
			cfg, err = LoadConfig(*cfgPath)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "加载配置失败：%v\n", err)
			fmt.Fprintf(os.Stderr, "提示：先运行 %s init -config %s 生成示例配置，再编辑上游地址。\n", filepath.Base(os.Args[0]), *cfgPath)
			pauseIfInteractive()
			os.Exit(1)
		}
	}
	if err := ValidateConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "配置校验失败：%v\n", err)
		fmt.Fprintf(os.Stderr, "请检查 %s 里的 upstreams / profiles / rules 字段。\n", *cfgPath)
		pauseIfInteractive()
		os.Exit(1)
	}

	bind, port := cfg.Server.Bind, cfg.Server.Port
	if *listen != "" {
		host, portStr, splitErr := net.SplitHostPort(*listen)
		if splitErr != nil {
			fmt.Fprintf(os.Stderr, "-listen 需要 host:port 形式：%v\n", splitErr)
			pauseIfInteractive()
			os.Exit(1)
		}
		n, convErr := strconv.Atoi(portStr)
		if convErr != nil {
			fmt.Fprintf(os.Stderr, "-listen 端口无效：%v\n", convErr)
			pauseIfInteractive()
			os.Exit(1)
		}
		bind, port = host, n
	}
	cfg.Server.Bind = bind
	cfg.Server.Port = port

	store := NewStore(cfg, *cfgPath)
	if cfg.Server.PersistState {
		path := *statePath
		if path == "" {
			path = defaultStatePath(*cfgPath)
		}
		store.SetStatePath(path)
		if err := store.LoadState(); err != nil {
			fmt.Fprintf(os.Stderr, "状态文件加载失败：%v（将以初始状态启动）\n", err)
		}
	} else {
		store.SetPersistEnabled(false)
		store.logf("INFO", "状态持久化已关闭")
	}
	srv := NewServer(store)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go srv.RunHealthLoop(ctx)
	go store.RunPersistLoop(ctx, time.Duration(cfg.Server.PersistIntervalSecond)*time.Second)

	addr := net.JoinHostPort(bind, strconv.Itoa(port))
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 20 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	base := "http://" + net.JoinHostPort(bind, strconv.Itoa(port))
	if bind == "0.0.0.0" || bind == "::" {
		base = "http://127.0.0.1:" + strconv.Itoa(port)
	}
	fmt.Println("========================================")
	fmt.Println(" 网关管家 gateway-manager " + version)
	fmt.Println(" 监听      : " + addr)
	fmt.Println(" OpenAI    : " + base + "/v1/chat/completions")
	fmt.Println(" Responses : " + base + "/v1/responses")
	fmt.Println(" Messages  : " + base + "/v1/messages")
	fmt.Println(" 模型列表  : " + base + "/v1/models")
	fmt.Println(" 控制台    : " + base + "/ui")
	fmt.Println(" 指标      : " + base + "/metrics")
	fmt.Println(" 上游数    : " + strconv.Itoa(len(cfg.Upstreams)) + "  策略数: " + strconv.Itoa(len(cfg.Profiles)) + "  规则数: " + strconv.Itoa(len(cfg.Rules)))
	if cfg.Server.PersistState {
		fmt.Println(" 状态文件  : " + defaultStatePath(*cfgPath))
	}
	fmt.Println(" 配置文件  : " + *cfgPath)
	fmt.Println("========================================")
	fmt.Println(" 按 Ctrl+C 停止服务")
	store.Info("网关启动：%s，上游 %d 个，策略 %d 个", addr, len(cfg.Upstreams), len(cfg.Profiles))

	if (*openUI || (bare && !*noBrowser)) && !interactiveIsService() {
		go func() {
			time.Sleep(400 * time.Millisecond)
			openBrowser(base + "/ui")
		}()
	}

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "监听失败：%v\n", err)
		fmt.Fprintf(os.Stderr, "提示：端口 %d 可能已被占用，可改用 -listen 127.0.0.1:8787\n", port)
		pauseIfInteractive()
		os.Exit(1)
	}
	store.Info("网关已停止")
}

// interactiveIsService：以 Windows 服务方式运行时不要弹浏览器
func interactiveIsService() bool {
	if os.Getenv("GATEWAY_MANAGER_NO_BROWSER") == "1" {
		return true
	}
	return !interactiveConsole()
}

// interactiveConsole 判断当前是否挂在交互式控制台（双击启动时为 true）
func interactiveConsole() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// pauseIfInteractive 在交互控制台里让窗口停留，避免双击后一闪而过
func pauseIfInteractive() {
	if !interactiveConsole() {
		return
	}
	if os.Getenv("GATEWAY_MANAGER_NO_PAUSE") == "1" {
		return
	}
	fmt.Fprint(os.Stderr, "\n按回车键退出...")
	var line string
	_, _ = fmt.Fscanln(os.Stdin, &line)
}

// openBrowser 尽力打开系统默认浏览器（失败静默）
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func runDiscover(args []string) {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	rangeSpec := fs.String("range", "", "端口范围，例如 3000-9999")
	usePriority := fs.Bool("priority", true, "先扫常用端口")
	workers := fs.Int("workers", 96, "并发数")
	asJSON := fs.Bool("json", false, "输出 JSON")
	timeout := fs.Duration("timeout", 120*time.Second, "整体超时")
	_ = fs.Parse(args)

	ports := make([]int, 0, 7200)
	seen := map[int]bool{}
	if *usePriority {
		for _, p := range PriorityPorts() {
			if !seen[p] {
				seen[p] = true
				ports = append(ports, p)
			}
		}
	}
	if *rangeSpec != "" {
		parts := strings.SplitN(*rangeSpec, "-", 2)
		if len(parts) != 2 {
			fmt.Fprintln(os.Stderr, "-range 需要形如 3000-9999")
			os.Exit(1)
		}
		from, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		to, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil || from > to {
			fmt.Fprintln(os.Stderr, "-range 参数无效")
			os.Exit(1)
		}
		for _, p := range PortRange(from, to) {
			if !seen[p] {
				seen[p] = true
				ports = append(ports, p)
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	fmt.Printf("扫描 127.0.0.1 上 %d 个端口...\n", len(ports))
	found := Discover(ctx, ports, *workers)

	if *asJSON {
		out, _ := json.MarshalIndent(found, "", "  ")
		fmt.Println(string(out))
		return
	}
	fmt.Print(FormatFound(found))
}

func runCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	cfgPath := fs.String("config", "config.json", "配置文件路径")
	_ = fs.Parse(args)

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败：%v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	fmt.Printf("%-14s %-22s %-6s %-8s %s\n", "ID", "名称", "结果", "耗时", "详情")
	for _, u := range cfg.Upstreams {
		if !u.Enabled {
			continue
		}
		item := u
		started := time.Now()
		key := ""
		if len(item.APIKeys) > 0 {
			key = item.APIKeys[0]
		}
		client := &http.Client{Timeout: 6 * time.Second}
		models, checkErr := fetchModels(ctx, client, &item, key, "")
		elapsed := time.Since(started).Milliseconds()
		result := "OK"
		detail := strconv.Itoa(len(models)) + " 个模型：" + strings.Join(models, ",")
		if checkErr != nil {
			result = "FAIL"
			detail = checkErr.Error()
		}
		fmt.Printf("%-14s %-22s %-6s %-8s %s\n", item.ID, item.Name, result, strconv.FormatInt(elapsed, 10)+"ms", detail)
	}
}

func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	cfgPath := fs.String("config", "config.json", "配置文件路径")
	force := fs.Bool("force", false, "覆盖已存在的配置")
	_ = fs.Parse(args)

	if !*force {
		if _, err := os.Stat(*cfgPath); err == nil {
			fmt.Fprintf(os.Stderr, "配置已存在：%s（加 -force 覆盖）\n", *cfgPath)
			os.Exit(1)
		}
	}
	if err := SaveConfig(*cfgPath, SampleConfig()); err != nil {
		fmt.Fprintf(os.Stderr, "写入配置失败：%v\n", err)
		os.Exit(1)
	}
	fmt.Printf("已生成示例配置：%s\n", *cfgPath)
}

func runDump(args []string) {
	fs := flag.NewFlagSet("dump", flag.ExitOnError)
	cfgPath := fs.String("config", "config.json", "配置文件路径")
	_ = fs.Parse(args)

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败：%v\n", err)
		os.Exit(1)
	}
	normalizeConfig(cfg)
	out, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println(string(out))
}
