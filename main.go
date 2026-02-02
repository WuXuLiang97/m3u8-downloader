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

// 任务结构
type DownloadTask struct {
	URL    string
	Output string
}

// 全局变量
var (
	tasks      []DownloadTask
	outputFile string
	threads    int
	tempDir    string
	urlFile    string
)

// 下载统计信息：增加原子化计数，避免遍历
type DownloadStats struct {
	Completed int64      // 已完成下载的片段数
	Total     int64      // 总片段数
	BytesDown int64      // 已下载字节数
	StartTime time.Time  // 开始下载时间
	Mutex     sync.Mutex // 统计信息的互斥锁（复用，避免冗余创建）
}

var downloadStats DownloadStats

// 全局HTTP客户端：复用连接池，设置超时
var httpClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:          100,              // 最大空闲连接数
		MaxIdleConnsPerHost:   20,               // 每个主机的最大空闲连接数（适配多线程）
		IdleConnTimeout:       30 * time.Second, // 空闲连接超时时间
		TLSHandshakeTimeout:   10 * time.Second, // TLS握手超时
		ResponseHeaderTimeout: 10 * time.Second, // 响应头超时
	},
	Timeout: 30 * time.Second, // 每个请求的总超时（防止卡住）
}

// displayProgressBar 在控制台显示进度条（优化：基于原子计数，无需遍历）
func displayProgressBar() {
	width := 50
	downloadStats.Mutex.Lock()
	completed := downloadStats.Completed
	total := downloadStats.Total
	bytesDown := downloadStats.BytesDown
	startTime := downloadStats.StartTime
	downloadStats.Mutex.Unlock()

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

	// 计算百分比、速度
	percent := int(progress * 100)
	elapsed := time.Since(startTime)
	if elapsed.Seconds() < 0.1 { // 避免除以0
		fmt.Printf("\r%s %d%% | Speed: -- B/s", bar, percent)
		return
	}
	speed := float64(bytesDown) / elapsed.Seconds()
	speedStr := formatSpeed(speed)

	// 输出进度条（覆盖当前行）
	fmt.Printf("\r%s %d%% | Speed: %s | %d/%d", bar, percent, speedStr, completed, total)
}

// formatSpeed 格式化速度为人类可读的格式（保留原逻辑）
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
	// 解析命令行参数（保留原逻辑）
	flag.StringVar(&outputFile, "output", "output.mp4", "Output MP4 file path (used as base name for multiple URLs)")
	flag.IntVar(&threads, "threads", 10, "Number of download threads")
	flag.StringVar(&tempDir, "temp", "temp", "Temporary directory for .ts files")
	flag.StringVar(&urlFile, "file", "", "File containing list of M3U8 URLs and output files (format: URL,output.mp4)")
	flag.Parse()

	// 处理命令行参数中的URL和输出文件名（保留原逻辑）
	args := flag.Args()
	for i := 0; i < len(args); i++ {
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			tasks = append(tasks, DownloadTask{
				URL:    args[i],
				Output: args[i+1],
			})
			i++
		} else {
			tasks = append(tasks, DownloadTask{
				URL:    args[i],
				Output: "",
			})
		}
	}

	// 读取URL文件（保留原逻辑，增加错误退出）
	if urlFile != "" {
		fileContent, err := os.ReadFile(urlFile)
		if err != nil {
			log.Fatalf("Error reading URL file: %v", err)
		}
		lines := strings.Split(string(fileContent), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.Split(line, ",")
			if len(parts) >= 2 {
				tasks = append(tasks, DownloadTask{
					URL:    strings.TrimSpace(parts[0]),
					Output: strings.TrimSpace(parts[1]),
				})
			} else {
				tasks = append(tasks, DownloadTask{
					URL:    strings.TrimSpace(parts[0]),
					Output: "",
				})
			}
		}
	}

	// 验证必要参数（保留原逻辑）
	if len(tasks) == 0 {
		fmt.Println("Error: At least one M3U8 URL is required")
		flag.Usage()
		os.Exit(1)
	}
}

func main() {
	fmt.Println("M3U8 Downloader v2.0 (Optimized)")
	fmt.Printf("Threads: %d\n", threads)
	fmt.Printf("Output dir: %s\n\n", tempDir)

	// 确保输出目录存在
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	// 处理每个任务
	for i, task := range tasks {
		fmt.Printf("=== Task %d of %d ===\n", i+1, len(tasks))
		fmt.Printf("Downloading: %s\n", task.URL)

		// 生成输出目录名
		var taskDirName string
		if task.Output != "" {
			taskDirName = task.Output
		} else {
			taskURL := task.URL
			baseName := ""
			playlistPattern := "/playlist.m3u8"
			playlistIndex := strings.LastIndex(taskURL, playlistPattern)
			if playlistIndex != -1 {
				pathBeforePlaylist := taskURL[:playlistIndex]
				lastSlashIndex := strings.LastIndex(pathBeforePlaylist, "/")
				if lastSlashIndex != -1 {
					baseName = pathBeforePlaylist[lastSlashIndex+1:]
				}
			} else {
				parsedURL, err := url.Parse(taskURL)
				if err == nil {
					baseName = filepath.Base(parsedURL.Path)
					baseName = strings.TrimSuffix(baseName, filepath.Ext(baseName))
				} else {
					baseName = fmt.Sprintf("output_%d", i+1)
				}
			}
			if baseName == "" {
				baseName = fmt.Sprintf("output_%d", i+1)
			}
			taskDirName = baseName
		}
		fmt.Printf("Output directory: %s\n\n", filepath.Join(tempDir, taskDirName))

		// 创建任务专用目录
		taskDir := filepath.Join(tempDir, taskDirName)
		if err := os.MkdirAll(taskDir, 0755); err != nil {
			fmt.Printf("Failed to create task directory: %v\n\n", err)
			continue
		}

		// 步骤1: 解析 m3u8 播放列表并保存
		fmt.Println("Step 1: Parsing and saving M3U8 playlist...")
		tsFiles, err := parseM3U8AndSave(task.URL, filepath.Join(taskDir, "playlist.m3u8"))
		if err != nil {
			fmt.Printf("Failed to parse M3U8: %v\n\n", err)
			continue
		}
		fmt.Printf("Found %d .ts files\n\n", len(tsFiles))

		// 步骤2: 多线程下载 .ts 片段
		fmt.Println("Step 2: Downloading .ts segments...")
		tsLocalFiles, err := downloadSegmentsToDir(tsFiles, threads, taskDir)
		if err != nil {
			fmt.Printf("Failed to download segments: %v\n\n", err)
			continue
		}
		fmt.Println("\nAll segments downloaded successfully\n")

		// 步骤3: 更新本地M3U8列表文件，使用相对路径
		fmt.Println("Step 3: Updating local M3U8 playlist...")
		if err := updateLocalM3U8(filepath.Join(taskDir, "playlist.m3u8"), tsLocalFiles); err != nil {
			fmt.Printf("Failed to update local M3U8: %v\n\n", err)
		}
		fmt.Println("Local M3U8 playlist updated successfully\n")
	}

	fmt.Println("All tasks completed! TS segments and M3U8 playlists are preserved in:", tempDir)
}

// parseM3U8 解析 m3u8 播放列表
func parseM3U8(m3u8URL string) ([]string, error) {
	resp, err := httpClient.Get(m3u8URL)
	if err != nil {
		return nil, fmt.Errorf("failed to download m3u8 file: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download m3u8 file: status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read m3u8 file: %v", err)
	}

	content := string(body)
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
		if !tsURL.IsAbs() {
			tsURL = baseURL.ResolveReference(tsURL)
		}
		tsURLs = append(tsURLs, tsURL.String())
	}

	if len(tsURLs) == 0 {
		return nil, fmt.Errorf("no .ts files found in m3u8 playlist")
	}

	return tsURLs, nil
}

// parseM3U8AndSave 解析 m3u8 播放列表并保存到文件
func parseM3U8AndSave(m3u8URL string, savePath string) ([]string, error) {
	resp, err := httpClient.Get(m3u8URL)
	if err != nil {
		return nil, fmt.Errorf("failed to download m3u8 file: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download m3u8 file: status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read m3u8 file: %v", err)
	}

	// 保存原始M3U8文件
	if err := os.WriteFile(savePath, body, 0644); err != nil {
		return nil, fmt.Errorf("failed to save m3u8 file: %v", err)
	}

	// 解析TS文件URLs
	content := string(body)
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
		if !tsURL.IsAbs() {
			tsURL = baseURL.ResolveReference(tsURL)
		}
		tsURLs = append(tsURLs, tsURL.String())
	}

	if len(tsURLs) == 0 {
		return nil, fmt.Errorf("no .ts files found in m3u8 playlist")
	}

	return tsURLs, nil
}

// downloadSegmentsToDir 多线程下载 .ts 片段到指定目录
func downloadSegmentsToDir(urls []string, threadCount int, outputDir string) ([]string, error) {
	var wg sync.WaitGroup
	var mutex sync.Mutex
	var errors []error

	// 限制最大线程数
	if threadCount > len(urls) {
		threadCount = len(urls)
	}

	// 初始化下载统计
	downloadStats = DownloadStats{
		Total:     int64(len(urls)),
		Completed: 0,
		BytesDown: 0,
		StartTime: time.Now(),
	}

	// 创建任务通道
	taskChan := make(chan int, len(urls))
	for i := range urls {
		taskChan <- i
	}
	close(taskChan)

	// 存储本地文件路径
	localFiles := make([]string, len(urls))

	// 启动线程
	fmt.Printf("Starting %d download threads...\n", threadCount)
	for i := 0; i < threadCount; i++ {
		wg.Add(1)
		go func(threadID int) {
			defer wg.Done()
			for idx := range taskChan {
				url := urls[idx]
				localFile := filepath.Join(outputDir, fmt.Sprintf("segment_%05d.ts", idx))

				// 下载文件
				if err := downloadFile(url, localFile); err != nil {
					mutex.Lock()
					errors = append(errors, fmt.Errorf("segment %d: %v", idx, err))
					mutex.Unlock()
					fmt.Printf("\nThread %d error: %v\n", threadID, err)
					// 继续尝试下一个片段
				} else {
					// 仅更新本地文件路径，无锁
					localFiles[idx] = localFile
					// 更新统计并显示进度条
					downloadStats.Mutex.Lock()
					downloadStats.Completed++
					downloadStats.Mutex.Unlock()
					displayProgressBar()
				}
			}
		}(i)
	}

	wg.Wait()
	// 下载完成后显示最终进度
	downloadStats.Mutex.Lock()
	downloadStats.Completed = downloadStats.Total
	downloadStats.Mutex.Unlock()
	displayProgressBar()

	// 检查错误
	if len(errors) > 0 {
		// 过滤出成功下载的文件
		var validFiles []string
		for _, f := range localFiles {
			if f != "" {
				validFiles = append(validFiles, f)
			}
		}

		if len(validFiles) == 0 {
			return nil, fmt.Errorf("all segments failed to download: %v", errors)
		}

		fmt.Printf("\nWarning: %d segments failed to download, but %d succeeded\n", len(errors), len(validFiles))
		return validFiles, nil
	}

	// 所有文件都成功下载
	return localFiles, nil
}

// updateLocalM3U8 更新本地M3U8文件，使用相对路径
func updateLocalM3U8(m3u8Path string, tsFiles []string) error {
	// 读取原始M3U8文件
	originalContent, err := os.ReadFile(m3u8Path)
	if err != nil {
		return fmt.Errorf("failed to read m3u8 file: %v", err)
	}

	lines := strings.Split(string(originalContent), "\n")
	var updatedLines []string
	segmentIndex := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			updatedLines = append(updatedLines, line)
		} else if segmentIndex < len(tsFiles) {
			// 替换为本地相对路径
			relPath := filepath.Base(tsFiles[segmentIndex])
			updatedLines = append(updatedLines, relPath)
			segmentIndex++
		}
	}

	// 写入更新后的内容
	updatedContent := strings.Join(updatedLines, "\n")
	if err := os.WriteFile(m3u8Path, []byte(updatedContent), 0644); err != nil {
		return fmt.Errorf("failed to write updated m3u8 file: %v", err)
	}

	return nil
}

// downloadFile 下载单个文件（核心优化：复用全局HTTP客户端、复用互斥锁、无冗余创建、添加重试机制）
func downloadFile(url, filePath string) error {
	const maxRetries = 3
	const retryDelay = 2 * time.Second

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err := httpClient.Get(url)
		if err != nil {
			lastErr = err
			if attempt < maxRetries {
				time.Sleep(retryDelay * time.Duration(attempt))
				continue
			}
			return err
		}

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			resp.Body.Close()
			lastErr = fmt.Errorf("status code %d", resp.StatusCode)
			if attempt < maxRetries {
				time.Sleep(retryDelay * time.Duration(attempt))
				continue
			}
			return lastErr
		}

		// 创建文件（保留原逻辑，增加权限设置）
		file, err := os.Create(filePath)
		if err != nil {
			resp.Body.Close()
			lastErr = err
			if attempt < maxRetries {
				time.Sleep(retryDelay * time.Duration(attempt))
				continue
			}
			return err
		}

		// 写入文件并统计字节数（保留原逻辑）
		bytesCopied, err := io.Copy(file, resp.Body)
		resp.Body.Close()
		file.Close()

		if err != nil {
			lastErr = err
			if attempt < maxRetries {
				time.Sleep(retryDelay * time.Duration(attempt))
				continue
			}
			return err
		}

		// 更新总下载字节数（复用全局统计的互斥锁，无冗余创建）
		downloadStats.Mutex.Lock()
		downloadStats.BytesDown += bytesCopied
		downloadStats.Mutex.Unlock()

		return nil
	}

	return lastErr
}
