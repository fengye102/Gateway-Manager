package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var version = "2.0.1"

const usageText = `
网关管家 pojiagw —— 本机网关聚合 + 策略注入（Windows / Linux 通用）

用法：
  pojiagw serve     [-config config.json] [-listen 127.0.0.1:7878]
  pojiagw discover  [-range 3000-9999] [-priority] [-workers 96] [-json]
  pojiagw check     [-config config.json]
  pojiagw init      [-config config.json] [-force]
  pojiagw dump      [-config config.json]
  pojiagw version

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
	case "serve", "run", "start":
		runServe(args)
	case "discover", "scan":
		runDiscover(args)
	case "check":
		runCheck(args)
	case "init":
		runInit(args)
	case "dump":
		runDump(args)
	case "version", "-v", "--version":
		fmt.Println("pojiagw " + version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令：%s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "config.json", "配置文件路径")
	listen := fs.String("listen", "", "覆盖监听地址，例如 127.0.0.1:7878")
	statePath := fs.String("state", "", "运行状态文件路径（key 健康分 / 规则命中），默认与配置同目录")
	_ = fs.Parse(args)

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败：%v\n先生成示例配置：pojiagw init -config %s\n", err, *cfgPath)
		os.Exit(1)
	}
	if err := ValidateConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "配置校验失败：%v\n", err)
		os.Exit(1)
	}

	bind, port := cfg.Server.Bind, cfg.Server.Port
	if *listen != "" {
		host, portStr, splitErr := net.SplitHostPort(*listen)
		if splitErr != nil {
			fmt.Fprintf(os.Stderr, "-listen 需要 host:port 形式：%v\n", splitErr)
			os.Exit(1)
		}
		n, convErr := strconv.Atoi(portStr)
		if convErr != nil {
			fmt.Fprintf(os.Stderr, "-listen 端口无效：%v\n", convErr)
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
	fmt.Println("========================================")
	fmt.Println(" 网关管家 pojiagw " + version)
	fmt.Println(" 监听      : " + addr)
	fmt.Println(" OpenAI    : " + base + "/v1/chat/completions")
	fmt.Println(" Responses : " + base + "/v1/responses")
	fmt.Println(" Messages  : " + base + "/v1/messages")
	fmt.Println(" 模型列表  : " + base + "/v1/models")
	fmt.Println(" 控制台    : " + base + "/ui")
	fmt.Println(" 上游数    : " + strconv.Itoa(len(cfg.Upstreams)) + "  策略数: " + strconv.Itoa(len(cfg.Profiles)) + "  规则数: " + strconv.Itoa(len(cfg.Rules)))
	if cfg.Server.PersistState {
		fmt.Println(" 状态文件  : " + defaultStatePath(*cfgPath))
	}
	fmt.Println("========================================")
	store.Info("网关启动：%s，上游 %d 个，策略 %d 个", addr, len(cfg.Upstreams), len(cfg.Profiles))

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "监听失败：%v\n", err)
		os.Exit(1)
	}
	store.Info("网关已停止")
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
