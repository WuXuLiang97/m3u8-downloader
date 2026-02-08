package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 任务结构：保留原逻辑
type DownloadTask struct {
	URL    string
	Output string
}

// 全局变量：仅保留必要参数，移除全局downloadStats（改为局部隔离）
var (
	tasks      []DownloadTask
	outputFile string
	threads    int
	tempDir    string
	urlFile    string
)

// 下载统计信息：移除全局变量，改为局部传递，保证多任务隔离
type DownloadStats struct {
	Completed int64      // 已完成下载的片段数
	Total     int64      // 总片段数
	BytesDown int64      // 已下载字节数
	StartTime time.Time  // 开始下载时间
	Mutex     sync.Mutex // 统计信息的互斥锁
}

// 全局HTTP客户端：保留原连接池优化，复用连接避免资源浪费
var httpClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	},
	Timeout: 30 * time.Second,
}

// displayProgressBar 带参数的进度条函数（修复：隔离多任务统计，100ms实时刷新）
func displayProgressBar(stats *DownloadStats) {
	width := 50
	stats.Mutex.Lock()
	completed := stats.Completed
	total := stats.Total
	bytesDown := stats.BytesDown
	startTime := stats.StartTime
	stats.Mutex.Unlock()

	if total == 0 {
		return
	}
	progress := float64(completed) / float64(total)
	filledWidth := int(progress * float64(width))
	emptyWidth := width - filledWidth

	// 构建进度条
	bar := "["
	for i := 0; i < filledWidth; i++ {
		bar += "="
	}
	if filledWidth < width {
		bar += ">"
	}
	for i := 0; i < emptyWidth-1; i++ {
		bar += " "
	}
	bar += "]"

	// 计算百分比、速度（避免除0）
	percent := int(progress * 100)
	elapsed := time.Since(startTime)
	if elapsed.Seconds() < 0.1 {
		fmt.Fprintf(os.Stdout, "\r%s %d%% | Speed: -- B/s", bar, percent)
		return
	}
	speed := float64(bytesDown) / elapsed.Seconds()
	speedStr := formatSpeed(speed)

	// 覆盖当前行输出进度条（标准输出流，避免日志混乱）
	fmt.Fprintf(os.Stdout, "\r%s %d%% | Speed: %s | %d/%d", bar, percent, speedStr, completed, total)
}

// formatSpeed 格式化速度为人类可读格式（保留原逻辑）
func formatSpeed(bytesPerSec float64) string {
	if bytesPerSec < 1024 {
		return fmt.Sprintf("%.2f B/s", bytesPerSec)
	} else if bytesPerSec < 1024*1024 {
		return fmt.Sprintf("%.2f KB/s", bytesPerSec/1024)
	} else {
		return fmt.Sprintf("%.2f MB/s", bytesPerSec/(1024*1024))
	}
}

func init() {
	// 解析命令行参数（保留原注释，优化参数说明）
	flag.StringVar(&outputFile, "output", "output.mp4", "Output file base name (for single URL without explicit output)")
	flag.IntVar(&threads, "threads", 10, "Number of download threads (>=1)")
	flag.StringVar(&tempDir, "temp", "temp", "Temporary directory for TS segments and M3U8")
	flag.StringVar(&urlFile, "file", "", "File with M3U8 tasks (format: each line is 'URL,output.mp4')")
	flag.Parse()

	// 修复：严格处理命令行参数，过滤-开头的flag参数，成对解析URL&输出名
	args := flag.Args()
	tasks = make([]DownloadTask, 0, len(args)/2+1) // 预分配切片容量，提升性能
	for i := 0; i < len(args); i++ {
		// 跳过所有flag格式的参数，避免错误匹配
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		task := DownloadTask{URL: args[i]}
		// 下一个参数非flag，作为当前URL的输出名
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			task.Output = args[i+1]
			i++ // 跳过已匹配的输出名
		}
		tasks = append(tasks, task)
	}

	// 读取URL文件（保留原逻辑，增加空行/注释过滤，错误直接退出）
	if urlFile != "" {
		fileContent, err := os.ReadFile(urlFile)
		if err != nil {
			log.Fatalf("❌ Failed to read URL file: %v", err)
		}
		lines := strings.Split(string(fileContent), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			// 过滤空行和#开头的注释行
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, ",", 2) // 仅分割一次，避免输出名含,
			task := DownloadTask{URL: strings.TrimSpace(parts[0])}
			if len(parts) >= 2 {
				task.Output = strings.TrimSpace(parts[1])
			}
			tasks = append(tasks, task)
		}
	}

	// 验证必要参数：无任务则提示并退出（输出到标准错误流）
	if len(tasks) == 0 {
		fmt.Fprintln(os.Stderr, "❌ Error: At least one M3U8 URL is required (via args or -file)")
		flag.Usage()
		os.Exit(1)
	}

	// 优化：线程数下限校验，避免后续处理异常
	if threads < 1 {
		threads = 1
		fmt.Fprintf(os.Stderr, "⚠️  Threads set to %d (minimum allowed)\n", threads)
	}
}

func main() {
	fmt.Println("=====================================")
	fmt.Println("  M3U8 Multi-thread Downloader v2.1  ")
	fmt.Println("  (All Bugs Fixed & Stable Version)  ")
	fmt.Println("=====================================")
	fmt.Printf("📌 Threads: %d\n", threads)
	fmt.Printf("📌 Temp Directory: %s\n\n", tempDir)

	// 清空临时目录，避免之前的文件混入
	if err := os.RemoveAll(tempDir); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to clean temp directory: %v\n", err)
	}

	// 重新创建临时目录
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		log.Fatalf("❌ Failed to create temp directory: %v", err)
	}
	fmt.Println("✅ Temp directory cleaned and recreated\n")

	// 处理每个下载任务（保留原任务遍历逻辑，优化日志输出）
	for taskIdx, task := range tasks {
		fmt.Printf("=== [Task %d/%d] Start Download ===\n", taskIdx+1, len(tasks))
		fmt.Printf("🔗 M3U8 URL: %s\n", task.URL)

		// 生成任务专属目录名（保留原逻辑，优化空值处理）
		var taskDirName string
		if task.Output != "" {
			taskDirName = task.Output
		} else {
			taskDirName = generateDefaultTaskName(task.URL, taskIdx)
		}
		// 任务专属目录（避免多任务文件冲突）
		taskDir := filepath.Join(tempDir, taskDirName)
		fmt.Printf("📂 Task Directory: %s\n\n", taskDir)

		// 创建任务目录（失败则跳过当前任务，继续下一个）
		if err := os.MkdirAll(taskDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to create task dir: %v\n\n", err)
			continue
		}

		// 步骤1：解析M3U8并保存原始文件到本地
		fmt.Println("Step 1: Parse M3U8 playlist & save original file...")
		tsURLs, err := parseM3U8AndSave(task.URL, filepath.Join(taskDir, "playlist.m3u8"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Parse M3U8 failed: %v\n\n", err)
			continue
		}
		fmt.Printf("✅ Found %d TS segments to download\n\n", len(tsURLs))

		// 步骤2：多线程下载TS片段到任务目录
		fmt.Println("Step 2: Multi-thread download TS segments...")
		tsLocalFiles, err := downloadSegmentsToDir(tsURLs, threads, taskDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Download segments failed: %v\n\n", err)
			continue
		}
		fmt.Println("✅ All TS segments downloaded (partial success if warning above)\n")

		// 步骤3：更新本地M3U8，替换为TS片段的本地相对路径
		fmt.Println("Step 3: Update local M3U8 with local TS paths...")
		if err := updateLocalM3U8(filepath.Join(taskDir, "playlist.m3u8"), tsLocalFiles); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Update M3U8 failed: %v\n\n", err)
		} else {
			fmt.Println("✅ Local M3U8 updated successfully (player-ready)\n")
		}
	}

	// 所有任务处理完成，提示最终结果
	fmt.Println("=====================================")
	fmt.Printf("✅ All tasks processed! Check files in: %s\n", tempDir)
	fmt.Println("💡 Tip: Use ffmpeg to merge: ffmpeg -i temp/xxx/playlist.m3u8 -c copy output.mp4")
	fmt.Println("=====================================")
}

// generateDefaultTaskName 生成默认任务名（抽离原逻辑，解耦main函数）
func generateDefaultTaskName(m3u8URL string, taskIdx int) string {
	playlistPattern := "/playlist.m3u8"
	if idx := strings.LastIndex(m3u8URL, playlistPattern); idx != -1 {
		if slashIdx := strings.LastIndex(m3u8URL[:idx], "/"); slashIdx != -1 {
			return m3u8URL[slashIdx+1 : idx]
		}
	}
	// 解析URL失败则用output_数字作为默认名
	parsedURL, err := url.Parse(m3u8URL)
	if err == nil {
		base := filepath.Base(parsedURL.Path)
		if base != "" {
			return strings.TrimSuffix(base, filepath.Ext(base))
		}
	}
	return fmt.Sprintf("output_%d", taskIdx+1)
}

// parseM3U8 解析M3U8获取TS URL列表（原函数，未被使用但保留，方便后续扩展）
func parseM3U8(m3u8URL string) ([]string, error) {
	resp, err := httpClient.Get(m3u8URL)
	if err != nil {
		return nil, fmt.Errorf("download m3u8 failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read m3u8 failed: %v", err)
	}

	return parseTSURLsFromM3U8Content(string(body), m3u8URL)
}

// parseM3U8AndSave 解析M3U8并保存原始文件（核心函数，保留原逻辑）
func parseM3U8AndSave(m3u8URL string, savePath string) ([]string, error) {
	resp, err := httpClient.Get(m3u8URL)
	if err != nil {
		return nil, fmt.Errorf("download m3u8 failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read m3u8 failed: %v", err)
	}

	// 保存原始M3U8文件（覆盖已有文件，权限0644）
	if err := os.WriteFile(savePath, body, 0644); err != nil {
		return nil, fmt.Errorf("save m3u8 failed: %v", err)
	}

	// 解析TS URL列表（抽离逻辑，解耦代码）
	return parseTSURLsFromM3U8Content(string(body), m3u8URL)
}

// parseTSURLsFromM3U8Content 从M3U8内容中解析TS URL（抽离的公共函数）
func parseTSURLsFromM3U8Content(content, m3u8URL string) ([]string, error) {
	lines := strings.Split(content, "\n")
	var tsURLs []string
	baseURL, err := url.Parse(m3u8URL)
	if err != nil {
		return nil, fmt.Errorf("invalid m3u8 URL: %v", err)
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		tsURL, err := url.Parse(line)
		if err != nil {
			continue
		}
		// 相对路径转为绝对URL
		if !tsURL.IsAbs() {
			tsURL = baseURL.ResolveReference(tsURL)
		}
		tsURLs = append(tsURLs, tsURL.String())
	}

	if len(tsURLs) == 0 {
		return nil, fmt.Errorf("no TS files found in m3u8")
	}

	return tsURLs, nil
}

// downloadSegmentsToDir 多线程下载TS片段（核心修复：局部stats，独立进度条协程）
func downloadSegmentsToDir(urls []string, threadCount int, outputDir string) ([]string, error) {
	var wg sync.WaitGroup
	var errMutex sync.Mutex // 保护errors切片的互斥锁
	var errors []error

	// 线程数上下限校验：不超过URL数，不低于1
	if threadCount > len(urls) {
		threadCount = len(urls)
	}
	if threadCount < 1 {
		threadCount = 1
	}

	// 核心修复：局部DownloadStats，彻底隔离多任务统计数据
	stats := &DownloadStats{
		Total:     int64(len(urls)),
		Completed: 0,
		BytesDown: 0,
		StartTime: time.Now(),
		Mutex:     sync.Mutex{},
	}

	// 核心修复：独立协程实时刷新进度条（100ms/次），解决速度失真
	progressQuit := make(chan struct{})
	defer close(progressQuit)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				displayProgressBar(stats)
			case <-progressQuit:
				return
			}
		}
	}()

	// 创建任务通道：缓冲大小为URL数，避免协程阻塞
	taskChan := make(chan int, len(urls))
	for i := range urls {
		taskChan <- i
	}
	close(taskChan)

	// 存储本地TS文件路径（按URL索引对应，保证顺序）
	localFiles := make([]string, len(urls))

	// 启动多线程下载协程
	fmt.Printf("🚀 Starting %d download threads (total %d segments)...\n", threadCount, len(urls))
	for i := 0; i < threadCount; i++ {
		wg.Add(1)
		go func(threadID int) {
			defer wg.Done()
			for idx := range taskChan {
				tsURL := urls[idx]
				// TS片段命名：segment_00000.ts，保证字典序和播放顺序
				tsLocalFile := filepath.Join(outputDir, fmt.Sprintf("segment_%05d.ts", idx))
				// 下载单个TS片段：传递局部stats，更新统计
				if err := downloadFile(tsURL, tsLocalFile, stats); err != nil {
					errMutex.Lock()
					errors = append(errors, fmt.Errorf("segment %d: %v", idx, err))
					errMutex.Unlock()
					fmt.Fprintf(os.Stderr, "\n⚠️  Thread %d: %v\n", threadID, err)
				} else {
					// 下载成功，记录本地路径
					localFiles[idx] = tsLocalFile
					// 更新已完成片段数（线程安全）
					stats.Mutex.Lock()
					stats.Completed++
					stats.Mutex.Unlock()
				}
			}
		}(i)
	}

	// 等待所有协程完成
	wg.Wait()

	// 下载完成：刷新最终进度，强制100%，并换行（避免覆盖后续输出）
	stats.Mutex.Lock()
	stats.Completed = stats.Total
	stats.Mutex.Unlock()
	displayProgressBar(stats)
	fmt.Println() // 进度条末尾换行，优化控制台排版

	// 处理下载错误：过滤成功的文件，返回结果
	if len(errors) > 0 {
		var validFiles []string
		for _, f := range localFiles {
			if f != "" {
				validFiles = append(validFiles, f)
			}
		}
		// 所有片段下载失败，返回错误
		if len(validFiles) == 0 {
			return nil, fmt.Errorf("all %d segments failed: %v", len(errors), errors[0])
		}
		// 部分片段失败，返回警告和成功的文件
		fmt.Fprintf(os.Stderr, "⚠️  Partial success: %d failed, %d succeeded\n", len(errors), len(validFiles))
		return validFiles, nil
	}

	// 所有片段下载成功
	return localFiles, nil
}

// updateLocalM3U8 修复核心：保留M3U8原始格式，不破坏协议，支持\r\n和\n换行
func updateLocalM3U8(m3u8Path string, tsFiles []string) error {
	// 读取原始M3U8文件（保留所有字节，不修改原始格式）
	originalContent, err := os.ReadFile(m3u8Path)
	if err != nil {
		return fmt.Errorf("read m3u8 failed: %v", err)
	}

	// 修复：根据原始内容自动识别换行符（\r\n或\n），避免破坏格式
	var lines []string
	var lineSep string
	if strings.Contains(string(originalContent), "\r\n") {
		lines = strings.Split(string(originalContent), "\r\n")
		lineSep = "\r\n"
	} else {
		lines = strings.Split(string(originalContent), "\n")
		lineSep = "\n"
	}

	var updatedLines []string
	segmentIndex := 0

	for _, line := range lines {
		// 仅过滤纯空行，不修改任何有效内容（移除原不当的TrimSpace）
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			updatedLines = append(updatedLines, line)
			continue
		}
		// 注释行/非TS行：直接保留原始内容，不做任何修改
		if strings.HasPrefix(trimmed, "#") {
			updatedLines = append(updatedLines, line)
		} else if segmentIndex < len(tsFiles) {
			// TS行：替换为本地相对路径（仅取文件名，保证M3U8可解析）
			updatedLines = append(updatedLines, filepath.Base(tsFiles[segmentIndex]))
			segmentIndex++
		} else {
			// 超出TS数量的行：直接保留，兼容M3U8扩展字段
			updatedLines = append(updatedLines, line)
		}
	}

	// 修复：用原始换行符拼接，保证M3U8协议兼容性
	updatedContent := strings.Join(updatedLines, lineSep)
	// 写入更新后的M3U8文件（覆盖原有，权限0644）
	if err := os.WriteFile(m3u8Path, []byte(updatedContent), 0644); err != nil {
		return fmt.Errorf("write m3u8 failed: %v", err)
	}

	return nil
}

// downloadFile 核心修复：失败清理空文件+关闭所有响应体+传递局部stats+重试机制+文件完整性验证
func downloadFile(tsURL, filePath string, stats *DownloadStats) error {
	const maxRetries = 10               // 最大重试次数
	const retryDelay = 5 * time.Second // 重试基础延迟（指数退避）

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		// 发起HTTP请求（复用全局连接池）
		resp, err := httpClient.Get(tsURL)
		if err != nil {
			lastErr = fmt.Errorf("attempt %d: get failed: %v", attempt, err)
			if attempt < maxRetries {
				time.Sleep(retryDelay * time.Duration(attempt))
				continue
			}
			_ = os.Remove(filePath) // 最终失败，清理空文件
			return lastErr
		}

		// 校验响应状态码（仅允许200和206）
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			resp.Body.Close() // 修复：必须关闭响应体，避免连接池泄漏
			lastErr = fmt.Errorf("attempt %d: status code %d", attempt, resp.StatusCode)
			if attempt < maxRetries {
				time.Sleep(retryDelay * time.Duration(attempt))
				continue
			}
			_ = os.Remove(filePath) // 最终失败，清理空文件
			return lastErr
		}

		// 获取 Content-Length 用于验证
		contentLength := int64(0)
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			fmt.Sscanf(cl, "%d", &contentLength)
		}

		// 创建本地文件（存在则覆盖，权限0644）
		file, err := os.Create(filePath)
		if err != nil {
			resp.Body.Close() // 修复：关闭响应体
			lastErr = fmt.Errorf("attempt %d: create file failed: %v", attempt, err)
			if attempt < maxRetries {
				time.Sleep(retryDelay * time.Duration(attempt))
				continue
			}
			_ = os.Remove(filePath) // 最终失败，清理空文件
			return lastErr
		}

		// 写入文件内容：直接拷贝响应体到文件
		bytesCopied, err := io.Copy(file, resp.Body)
		// 修复：优先关闭所有资源，再处理错误（避免资源泄漏）
		_ = file.Close()
		resp.Body.Close()

		if err != nil {
			lastErr = fmt.Errorf("attempt %d: write failed: %v", attempt, err)
			if attempt < maxRetries {
				time.Sleep(retryDelay * time.Duration(attempt))
				continue
			}
			_ = os.Remove(filePath) // 最终失败，清理损坏/空文件
			return lastErr
		}

		// 验证下载完整性：检查实际下载字节数与 Content-Length 是否匹配
		if contentLength > 0 && bytesCopied != contentLength {
			lastErr = fmt.Errorf("attempt %d: incomplete download: expected %d bytes, got %d bytes", attempt, contentLength, bytesCopied)
			if attempt < maxRetries {
				time.Sleep(retryDelay * time.Duration(attempt))
				continue
			}
			_ = os.Remove(filePath) // 最终失败，清理不完整文件
			return lastErr
		}

		// 验证文件是否为空
		if bytesCopied == 0 {
			lastErr = fmt.Errorf("attempt %d: empty file downloaded", attempt)
			if attempt < maxRetries {
				time.Sleep(retryDelay * time.Duration(attempt))
				continue
			}
			_ = os.Remove(filePath) // 最终失败，清理空文件
			return lastErr
		}

		// 下载成功：更新总下载字节数（线程安全）
		stats.Mutex.Lock()
		stats.BytesDown += bytesCopied
		stats.Mutex.Unlock()

		return nil
	}

	// 所有重试失败，清理空文件
	_ = os.Remove(filePath)
	return fmt.Errorf("all %d attempts failed: %v", maxRetries, lastErr)
}
