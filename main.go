package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxLogEntries = 500
	arkBaseURL    = "https://ark.cn-beijing.volces.com/api/coding"
	arkAPIKey     = "ffa78d28-8524-4372-8e0e-1bd873e38b26"
	arkModel      = "glm-4-7-251222"
)

type App struct {
	baseDir      string
	configFile   string
	logsFile     string
	serverLogDir string

	mu sync.Mutex
}

type NewsItem struct {
	Date        string `json:"date"`
	Theme       string `json:"theme"`
	ThemeFolder string `json:"theme_folder"`
	Filename    string `json:"filename"`
	Path        string `json:"path"`
	Mtime       string `json:"mtime"`
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("获取工作目录失败: %v", err)
	}

	app := &App{
		baseDir:      cwd,
		configFile:   filepath.Join(cwd, "config.json"),
		logsFile:     filepath.Join(cwd, "logs.json"),
		serverLogDir: filepath.Join(cwd, "logs"),
	}

	if err := os.MkdirAll(app.serverLogDir, 0o755); err != nil {
		log.Fatalf("创建日志目录失败: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handle)

	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8000"
	}
	app.logf("INFO", "%s", strings.Repeat("=", 60))
	app.logf("INFO", "新闻查询配置系统 (Go 版本)")
	app.logf("INFO", "访问地址: http://localhost:%s", port)
	app.logf("INFO", "服务端日志目录: %s", app.serverLogDir)
	app.logf("INFO", "%s", strings.Repeat("=", 60))

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 360 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		app.logf("ERROR", "服务启动失败: %v", err)
		os.Exit(1)
	}
}

func (a *App) handle(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		a.logf("INFO", "%s %s cost_ms=%d", r.Method, r.URL.Path, time.Since(start).Milliseconds())
	}()

	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/":
			a.serveStaticFile(w, r, "index.html")
			return
		case "/config.json":
			cfg, err := a.loadConfig()
			if err != nil {
				a.writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
				return
			}
			a.writeJSON(w, http.StatusOK, cfg)
			return
		case "/news_list":
			a.handleNewsList(w)
			return
		case "/logs":
			logs, err := a.loadLogs()
			if err != nil {
				a.writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
				return
			}
			a.writeJSON(w, http.StatusOK, map[string]any{"success": true, "logs": logs})
			return
		}

		a.serveStaticPath(w, r)
		return
	}

	if r.Method == http.MethodPost {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			a.writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "无效的JSON数据"})
			return
		}

		switch r.URL.Path {
		case "/save_config":
			if err := a.saveConfig(req); err != nil {
				a.writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
				return
			}
			themes := getArray(req, "themes")
			a.logf("INFO", "配置已保存，主题数量=%d", len(themes))
			a.writeJSON(w, http.StatusOK, map[string]any{"success": true})
			return
		case "/delete_news":
			a.handleDeleteNews(w, req)
			return
		case "/run_query":
			a.handleRunQuery(w, req)
			return
		case "/parse_time":
			a.handleParseTime(w, req)
			return
		case "/add_log":
			if err := a.saveLog(req); err != nil {
				a.writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
				return
			}
			a.writeJSON(w, http.StatusOK, map[string]any{"success": true})
			return
		default:
			a.writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "接口不存在"})
			return
		}
	}

	a.writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "方法不允许"})
}

func (a *App) handleNewsList(w http.ResponseWriter) {
	cfg, err := a.loadConfig()
	if err != nil {
		a.writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	settings := getMap(cfg, "settings")
	outputBase := getStringWithDefault(settings, "output_base_path", "/Users/bytedance/news_output")
	themes := getArray(cfg, "themes")

	themeMap := make(map[string]string)
	for _, t := range themes {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		folder := getString(m, "folder")
		name := getString(m, "name")
		if folder != "" && name != "" {
			themeMap[folder] = name
		}
	}

	var items []NewsItem
	dateRegex := regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`)
	basePath := filepath.Clean(outputBase)

	entries, err := os.ReadDir(basePath)
	if err == nil {
		for _, folder := range entries {
			if !folder.IsDir() {
				continue
			}
			folderName := folder.Name()
			themeName := folderName
			if mapped, ok := themeMap[folderName]; ok {
				themeName = mapped
			}

			dirPath := filepath.Join(basePath, folderName)
			htmlFiles, _ := os.ReadDir(dirPath)
			for _, f := range htmlFiles {
				if f.IsDir() || !strings.HasSuffix(strings.ToLower(f.Name()), ".html") {
					continue
				}
				dateStr := "未知日期"
				if m := dateRegex.FindStringSubmatch(f.Name()); len(m) == 2 {
					dateStr = m[1]
				} else if strings.HasPrefix(strings.TrimSuffix(f.Name(), ".html"), "news_") {
					dateStr = strings.TrimPrefix(strings.TrimSuffix(f.Name(), ".html"), "news_")
				}

				absPath := filepath.Join(dirPath, f.Name())
				st, statErr := os.Stat(absPath)
				mtime := ""
				if statErr == nil {
					mtime = st.ModTime().Format(time.RFC3339)
				}
				items = append(items, NewsItem{
					Date:        dateStr,
					Theme:       themeName,
					ThemeFolder: folderName,
					Filename:    f.Name(),
					Path:        absPath,
					Mtime:       mtime,
				})
			}
		}
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Date > items[j].Date })

	grouped := make(map[string][]map[string]any)
	for _, item := range items {
		grouped[item.Theme] = append(grouped[item.Theme], map[string]any{
			"date":         item.Date,
			"theme":        item.Theme,
			"theme_folder": item.ThemeFolder,
			"filename":     item.Filename,
			"path":         item.Path,
			"mtime":        item.Mtime,
		})
	}

	a.writeJSON(w, http.StatusOK, map[string]any{"success": true, "news": grouped, "total": len(items)})
}

func (a *App) handleDeleteNews(w http.ResponseWriter, req map[string]any) {
	path := getString(req, "path")
	if path == "" {
		a.writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "缺少文件路径"})
		return
	}
	if _, err := os.Stat(path); err != nil {
		a.writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "文件不存在"})
		return
	}
	if err := os.Remove(path); err != nil {
		a.writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	a.logf("INFO", "删除新闻文件: %s", path)
	a.writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (a *App) handleRunQuery(w http.ResponseWriter, req map[string]any) {
	themeID := normalizeID(req["theme_id"])
	if themeID == "" {
		a.writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "缺少theme_id"})
		return
	}

	cfg, err := a.loadConfig()
	if err != nil {
		a.writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	themes := getArray(cfg, "themes")

	var theme map[string]any
	for _, t := range themes {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if normalizeID(m["id"]) == themeID {
			theme = m
			break
		}
	}
	if theme == nil {
		a.writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "主题不存在"})
		return
	}

	themeName := getString(theme, "name")
	a.logf("INFO", "开始执行查询，theme_id=%s, theme_name=%s", themeID, themeName)

	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	settings := getMap(cfg, "settings")
	outputBase := getStringWithDefault(settings, "output_base_path", "/Users/bytedance/news_output")
	folder := getString(theme, "folder")
	folderPath := filepath.Join(outputBase, folder)
	if err := os.MkdirAll(folderPath, 0o755); err != nil {
		a.writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	outputFile := filepath.Join(folderPath, yesterday+".html")

	opencodePath := getOpenCodePath()
	userPrompt := getString(theme, "prompt")
	minCount := getInt(theme, "min_news_count")

	minRequirement := ""
	if minCount > 0 {
		minRequirement = fmt.Sprintf("\n最少需要找到 %d 条新新闻，如果少于这个数量请不要添加。", minCount)
	}

	appendInstruction := "创建新文件并写入所有找到的新闻。"
	if _, err := os.Stat(outputFile); err == nil {
		appendInstruction = fmt.Sprintf("文件 %s 已经存在，请将新找到的新闻追加到已有内容后面，**注意不要重复添加已有的新闻**，去重后保留所有不重复的新闻。", outputFile)
	}

	prompt := fmt.Sprintf(`查询前一天新闻，主题为%s。
用户对该主题的新闻要求/提示词：%s%s
查询日期范围：%s（前一天）
文件名：%s.html，保存在%s/%s/下。
每条新闻需要包含：
1. 新闻标题
2. 发布来源
3. 发布时间
4. 简要内容摘要
5. **原始新闻链接URL**（必须包含，方便用户点击查看原文）

%s
请整理成清晰的HTML格式。
注意：任何权限我都同意直接执行，不需要询问确认。请直接执行所有必要的操作，包括网络搜索、文件读写等。`,
		themeName,
		userPrompt,
		minRequirement,
		yesterday,
		yesterday,
		outputBase,
		folder,
		appendInstruction,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, opencodePath, "run", prompt)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard

	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		a.writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "查询执行超时", "details": "超过300秒"})
		return
	}
	if err != nil {
		a.logf("ERROR", "查询执行失败，theme=%s, stderr=%s", themeName, stderr.String())
		a.writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "查询执行失败", "details": stderr.String()})
		return
	}

	message := "查询请求已处理"
	if minCount > 0 {
		if _, err := os.Stat(outputFile); err == nil {
			actualCount := countNewsInFile(outputFile)
			if actualCount < minCount {
				_ = os.Remove(outputFile)
				message = fmt.Sprintf("查询完成但仅找到 %d 条新闻，少于要求的 %d 条，已删除文件", actualCount, minCount)
				a.writeJSON(w, http.StatusOK, map[string]any{
					"success":      false,
					"error":        message,
					"actual_count": actualCount,
					"min_count":    minCount,
				})
				return
			}
		}
	}

	a.logf("INFO", "查询执行完成，theme=%s, output=%s", themeName, outputFile)
	a.writeJSON(w, http.StatusOK, map[string]any{"success": true, "output": outputFile, "message": message})
}

func (a *App) handleParseTime(w http.ResponseWriter, req map[string]any) {
	traceID := randomTraceID()
	requestStart := time.Now()
	description := strings.TrimSpace(getString(req, "description"))
	if description == "" {
		a.writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "缺少 description"})
		return
	}

	a.logf("INFO", "[parse_time][%s] 解析描述: %s", traceID, description)
	output, err := a.callArkTimeAgent(description, traceID)
	if err != nil {
		a.logf("ERROR", "[parse_time][%s] 异常: %v", traceID, err)
		errMsg := err.Error()
		status := http.StatusInternalServerError
		if strings.Contains(strings.ToLower(errMsg), "timeout") {
			errMsg = "解析超时，Agent API 响应时间超过60秒"
		}
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(output)), "ERROR:") {
			status = http.StatusBadRequest
			errMsg = strings.TrimSpace(output)
		}
		a.writeJSON(w, status, map[string]any{"success": false, "error": errMsg, "raw_output": output})
		return
	}

	a.logf("INFO", "[parse_time][%s] AI输出(原始): %s", traceID, output)

	trimmed := strings.TrimSpace(output)
	if strings.HasPrefix(strings.ToUpper(trimmed), "ERROR:") {
		a.writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": trimmed, "raw_output": output})
		return
	}

	cronRe := regexp.MustCompile(`([0-9\*/,\-]+\s+[0-9\*/,\-]+\s+[0-9\*/,\-]+\s+[0-9\*/,\-]+\s+[0-9\*/,\-]+)`)
	match := cronRe.FindStringSubmatch(output)
	if len(match) < 2 {
		a.writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "模型返回无法识别为 cron 表达式，请换一种时间描述再试", "raw_output": output})
		return
	}

	cronExpr := strings.TrimSpace(match[1])
	parts := strings.Fields(cronExpr)
	var hourVal any = nil
	var minuteVal any = nil
	if len(parts) == 5 && isDigits(parts[0]) && isDigits(parts[1]) {
		minute, _ := strconv.Atoi(parts[0])
		hour, _ := strconv.Atoi(parts[1])
		if minute >= 0 && minute <= 59 && hour >= 0 && hour <= 23 {
			hourVal = hour
			minuteVal = minute
		}
	}

	a.logf("INFO", "[parse_time][%s] 解析结果: cron=%s, hour=%v, minute=%v, display=%s", traceID, cronExpr, hourVal, minuteVal, cronExpr)

	entry := map[string]any{
		"type":        "parse_time",
		"trace_id":    traceID,
		"description": description,
		"result":      cronExpr,
		"output":      output,
		"timestamp":   time.Now().Format(time.RFC3339),
	}
	if err := a.saveLog(entry); err != nil {
		a.logf("ERROR", "[parse_time][%s] 保存日志失败: %v", traceID, err)
	} else {
		a.logf("INFO", "[parse_time][%s] 操作日志已保存, total_cost_ms=%d", traceID, time.Since(requestStart).Milliseconds())
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"cron":    cronExpr,
		"hour":    hourVal,
		"minute":  minuteVal,
		"display": cronExpr,
	})
}

func (a *App) callArkTimeAgent(userDescription, traceID string) (string, error) {
	systemPrompt := `你是时间表达式解析助手。
将用户中文自然语言时间描述解析为 cron 表达式（5段格式：分 时 日 月 周）。
你必须只返回一行 cron 表达式，不要解释，不要 JSON，不要代码块。
例如：
0 10 * * *
30 8 * * *
*/15 * * * *
如果无法解析，返回:
ERROR: 无法解析`
	userPrompt := fmt.Sprintf("当前时间: %s\n用户描述: %s", time.Now().Format("2006-01-02 15:04:05"), userDescription)

	a.logf("INFO", "[parse_time][%s] Agent请求输入: model=%s, base=%s, description=%s", traceID, arkModel, arkBaseURL, userDescription)
	a.logf("INFO", "[parse_time][%s] Agent system prompt: %s", traceID, systemPrompt)
	a.logf("INFO", "[parse_time][%s] Agent user prompt: %s", traceID, userPrompt)

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + arkAPIKey,
	}

	responsesPayload := map[string]any{
		"model": arkModel,
		"input": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"temperature": 0.1,
	}

	startResponses := time.Now()
	status, body, err := postJSON(arkBaseURL+"/v1/responses", headers, responsesPayload, 60*time.Second)
	responsesCost := time.Since(startResponses).Milliseconds()
	bodyText := string(body)
	a.logf("INFO", "[parse_time][%s] Agent responses状态: status=%d, cost_ms=%d, body=%s", traceID, status, responsesCost, bodyText)
	if err == nil && status == http.StatusOK {
		text := extractTextFromResponses(body)
		if strings.TrimSpace(text) != "" {
			a.logf("INFO", "[parse_time][%s] Agent最终输出(来自responses): %s", traceID, text)
			return text, nil
		}
		return bodyText, fmt.Errorf("Agent API 返回成功但未包含可解析文本")
	}

	fallbackPayload := map[string]any{
		"model": arkModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"max_tokens":  256,
		"temperature": 0.1,
	}

	startFallback := time.Now()
	fStatus, fBody, fErr := postJSON(arkBaseURL+"/v1/chat/completions", headers, fallbackPayload, 60*time.Second)
	fallbackCost := time.Since(startFallback).Milliseconds()
	fBodyText := string(fBody)
	a.logf("INFO", "[parse_time][%s] chat/completions回退状态: status=%d, cost_ms=%d, body=%s", traceID, fStatus, fallbackCost, fBodyText)
	if fErr != nil {
		if err != nil {
			return strings.TrimSpace(bodyText + "\n" + fBodyText), fmt.Errorf("responses错误: %v; fallback错误: %v", err, fErr)
		}
		return fBodyText, fErr
	}
	if fStatus != http.StatusOK {
		return fBodyText, fmt.Errorf("Agent API失败(%d)，chat/completions回退失败(%d): %s", status, fStatus, truncate(fBodyText, 500))
	}

	finalText := extractTextFromChat(fBody)
	if strings.TrimSpace(finalText) == "" {
		return fBodyText, fmt.Errorf("chat/completions返回为空")
	}
	a.logf("INFO", "[parse_time][%s] Agent最终输出(来自fallback): %s", traceID, finalText)
	return finalText, nil
}

func extractTextFromResponses(body []byte) string {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if outputText, ok := payload["output_text"].(string); ok && strings.TrimSpace(outputText) != "" {
		return strings.TrimSpace(outputText)
	}

	outputs, ok := payload["output"].([]any)
	if !ok {
		return ""
	}
	for _, item := range outputs {
		im, ok := item.(map[string]any)
		if !ok {
			continue
		}
		contents, ok := im["content"].([]any)
		if !ok {
			continue
		}
		for _, c := range contents {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := cm["type"].(string)
			if typ == "output_text" || typ == "text" {
				if text, ok := cm["text"].(string); ok && strings.TrimSpace(text) != "" {
					return strings.TrimSpace(text)
				}
			}
		}
	}
	return ""
}

func extractTextFromChat(body []byte) string {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	choices, ok := payload["choices"].([]any)
	if !ok || len(choices) == 0 {
		return ""
	}
	first, ok := choices[0].(map[string]any)
	if !ok {
		return ""
	}
	msg, ok := first["message"].(map[string]any)
	if !ok {
		return ""
	}
	content, _ := msg["content"].(string)
	return strings.TrimSpace(content)
}

func postJSON(url string, headers map[string]string, payload any, timeout time.Duration) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resp.StatusCode, nil, readErr
	}
	return resp.StatusCode, respBody, nil
}

func (a *App) loadConfig() (map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, err := os.Stat(a.configFile); err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), nil
		}
		return nil, err
	}
	data, err := os.ReadFile(a.configFile)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (a *App) saveConfig(data map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.configFile, b, 0o644)
}

func (a *App) loadLogs() ([]map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := os.Stat(a.logsFile); err != nil {
		if os.IsNotExist(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	data, err := os.ReadFile(a.logsFile)
	if err != nil {
		return nil, err
	}
	var entries []map[string]any
	if err := json.Unmarshal(data, &entries); err != nil {
		return []map[string]any{}, nil
	}
	return entries, nil
}

func (a *App) saveLog(entry map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	entries := []map[string]any{}
	if data, err := os.ReadFile(a.logsFile); err == nil {
		_ = json.Unmarshal(data, &entries)
	}
	entries = append(entries, entry)
	if len(entries) > maxLogEntries {
		entries = entries[len(entries)-maxLogEntries:]
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.logsFile, b, 0o644)
}

func defaultConfig() map[string]any {
	return map[string]any{
		"themes": []any{},
		"settings": map[string]any{
			"query_time":       "09:00",
			"output_base_path": "/Users/bytedance/news_output",
			"cron_schedule":    "0 9 * * *",
		},
	}
}

func getOpenCodePath() string {
	if path, err := exec.LookPath("opencode"); err == nil && path != "" {
		return path
	}
	candidates := []string{
		"/usr/local/bin/opencode",
		"/usr/bin/opencode",
		"opencode",
	}
	for _, p := range candidates {
		if strings.HasPrefix(p, "/") {
			if _, err := os.Stat(p); err == nil {
				return p
			}
			continue
		}
		return p
	}
	return "opencode"
}

func countNewsInFile(filePath string) int {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return 0
	}
	content := string(data)
	headlineRe := regexp.MustCompile(`(?i)<h[1-3][^>]*>`)
	itemRe := regexp.MustCompile(`(?i)class="[^"]*(news|item|article)[^"]*"`)
	headlines := len(headlineRe.FindAllString(content, -1))
	items := len(itemRe.FindAllString(content, -1))
	mx := headlines
	if items > mx {
		mx = items
	}
	if mx == 0 {
		return 1
	}
	return mx
}

func (a *App) writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

func (a *App) serveStaticFile(w http.ResponseWriter, r *http.Request, rel string) {
	p := filepath.Join(a.baseDir, rel)
	http.ServeFile(w, r, p)
}

func (a *App) serveStaticPath(w http.ResponseWriter, r *http.Request) {
	cleaned := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if cleaned == "." || cleaned == "" {
		cleaned = "index.html"
	}
	fullPath := filepath.Join(a.baseDir, cleaned)
	if !strings.HasPrefix(fullPath, a.baseDir) {
		http.NotFound(w, r)
		return
	}
	if st, err := os.Stat(fullPath); err == nil && !st.IsDir() {
		if ct := mime.TypeByExtension(filepath.Ext(fullPath)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		http.ServeFile(w, r, fullPath)
		return
	}
	if st, err := os.Stat(fullPath); err == nil && st.IsDir() {
		if _, err := fs.Stat(os.DirFS(fullPath), "index.html"); err == nil {
			http.ServeFile(w, r, filepath.Join(fullPath, "index.html"))
			return
		}
	}
	http.NotFound(w, r)
}

func (a *App) logf(level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("%s [%s] news_query_server - %s", time.Now().Format("2006-01-02 15:04:05"), level, msg)
	log.Println(line)
	_ = os.MkdirAll(a.serverLogDir, 0o755)
	path := filepath.Join(a.serverLogDir, fmt.Sprintf("server-%s.log", time.Now().Format("2006-01-02")))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line + "\n")
}

func randomTraceID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)[:8]
	}
	return hex.EncodeToString(b)
}

func getMap(m map[string]any, key string) map[string]any {
	v, ok := m[key]
	if !ok {
		return map[string]any{}
	}
	mm, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return mm
}

func getArray(m map[string]any, key string) []any {
	v, ok := m[key]
	if !ok {
		return []any{}
	}
	arr, ok := v.([]any)
	if !ok {
		return []any{}
	}
	return arr
}

func getString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func getStringWithDefault(m map[string]any, key, def string) string {
	s := getString(m, key)
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func getInt(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	default:
		return 0
	}
}

func normalizeID(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case string:
		return strings.TrimSpace(strings.TrimSuffix(x, ".0"))
	default:
		str := fmt.Sprintf("%v", v)
		return strings.TrimSpace(strings.TrimSuffix(str, ".0"))
	}
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n]
}
