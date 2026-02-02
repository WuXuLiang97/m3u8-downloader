# M3U8 多线程下载器

[![Go Version](https://img.shields.io/badge/Go-1.20+-blue.svg)](https://golang.org/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

一个高性能、多线程的 M3U8 视频下载工具，用于下载 M3U8 格式的视频播放列表及其对应的 TS 切片文件。

## ✨ 功能特性

- 🚀 **多线程并行下载**：显著提高下载速度
- 📁 **保留原始文件结构**：完整保留 TS 切片和 M3U8 列表文件
- 🔄 **网络错误自动重试**：应对不稳定网络环境
- 📊 **实时进度显示**：实时显示下载进度、速度和完成百分比
- 📋 **多任务支持**：一次处理多个下载任务
- 📝 **任务文件支持**：从文件读取任务列表，方便批量下载
- 🔍 **智能目录命名**：基于 URL 路径自动生成有意义的目录名
- 🌐 **协议兼容**：支持 HTTP 206 Partial Content 响应
- 🛡️ **安全可靠**：完善的错误处理和资源泄漏防护

## 📦 安装

### 方法 1：直接下载可执行文件

从 [GitHub Releases](https://github.com/WuXuLiang97/m3u8-downloader/releases) 下载预编译的可执行文件。

### 方法 2：从源码编译

1. **安装 Go 环境**（推荐 Go 1.20+）
2. **克隆仓库**：
   ```bash
   git clone https://github.com/WuXuLiang97/m3u8-downloader.git
   cd m3u8-downloader
   ```
3. **编译**：
   ```bash
   go build -o m3u8-downloader.exe main.go
   ```

## 🚀 使用方法

### 基本命令格式

```bash
m3u8-downloader.exe [参数] [URL1] [输出目录1] [URL2] [输出目录2] ...
```

### 参数说明

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-threads` | 下载线程数 | 10 |
| `-output` | 默认输出目录名（未指定时使用） | output |
| `-temp` | 输出根目录 | temp |
| `-file` | 包含任务列表的文件路径 | 无 |

### 使用示例

#### 1. 单个任务下载

```bash
# 指定 URL 和输出目录
m3u8-downloader.exe -threads 5 "https://example.com/playlist.m3u8" "my_video"

# 使用默认输出目录
m3u8-downloader.exe -threads 10 "https://example.com/playlist.m3u8"
```

#### 2. 多个任务下载

```bash
# 命令行直接指定多个任务
m3u8-downloader.exe -threads 8 "https://example.com/playlist1.m3u8" "video1" "https://example.com/playlist2.m3u8" "video2"

# 从文件读取任务列表
m3u8-downloader.exe -file "tasks.txt" -threads 10
```

### 任务文件格式

创建一个 `tasks.txt` 文件，每行一个任务，格式为：

```
# 注释行（以 # 开头）
https://example.com/playlist1.m3u8,output_dir1
https://example.com/playlist2.m3u8,output_dir2

# 空行也会被忽略
https://example.com/playlist3.m3u8
```

## 📁 输出目录结构

下载完成后，文件将保存在以下结构中：

```
temp/
├── output_dir1/          # 任务1的输出目录
│   ├── playlist.m3u8     # 更新后的本地 M3U8 文件（使用相对路径）
│   ├── segment_00000.ts  # TS 片段文件
│   ├── segment_00001.ts
│   └── ...
└── output_dir2/          # 任务2的输出目录
    ├── playlist.m3u8
    ├── segment_00000.ts
    └── ...
```

## 🎯 高级用法

### 1. 合并为 MP4

下载完成后，可以使用 FFmpeg 将 TS 片段合并为 MP4：

```bash
# 使用本地 M3U8 文件合并
ffmpeg -i temp/output_dir/playlist.m3u8 -c copy output.mp4

# 直接使用原始 M3U8 URL 合并
ffmpeg -i "https://example.com/playlist.m3u8" -c copy output.mp4
```

### 2. 批量下载配置

创建一个详细的任务文件，包含多个下载任务：

**tasks.txt**
```
# 动漫系列
https://example.com/anime/01/playlist.m3u8,anime_ep01
https://example.com/anime/02/playlist.m3u8,anime_ep02
https://example.com/anime/03/playlist.m3u8,anime_ep03

# 电影
https://example.com/movie/playlist.m3u8,movie
```

### 3. 最佳线程数设置

- **网络速度快**：使用较多线程（15-20）
- **网络不稳定**：使用较少线程（5-10）
- **默认设置**：10 线程（平衡速度和稳定性）

## 🔧 技术架构

### 核心模块

1. **命令行解析**：解析参数和任务列表
2. **M3U8 解析**：下载并解析 M3U8 播放列表
3. **多线程下载**：并发下载 TS 片段
4. **文件管理**：创建目录和管理本地文件
5. **错误处理**：网络错误重试和异常处理

### 技术实现

- **并发模型**：Goroutines + Channels
- **网络请求**：复用 HTTP 连接池
- **进度显示**：独立的进度条 Goroutine
- **错误处理**：指数退避重试机制
- **文件操作**：原子化文件写入

## 🛠️ 代码结构

```
├── main.go          # 主程序实现
├── go.mod           # Go 模块定义
├── README.md        # 项目说明文档
├── help.html        # 详细帮助文档
├── tasks.txt        # 任务列表示例
└── m3u8-downloader.exe  # 编译后的可执行文件
```

### 核心函数

- **parseM3U8AndSave**：解析 M3U8 并保存原始文件
- **downloadSegmentsToDir**：多线程下载 TS 片段
- **updateLocalM3U8**：更新本地 M3U8 文件
- **downloadFile**：下载单个 TS 片段（含重试机制）

## 📝 常见问题

### 1. 下载失败，显示 "status code 206" 错误

这表示服务器返回了部分内容（Partial Content），这是正常的响应，工具已经处理了这种情况。

### 2. 有些 TS 片段没有下载成功

工具会显示成功和失败的片段数量。即使部分片段失败，也会继续执行并保存已成功下载的片段。建议：
- 检查网络连接是否稳定
- 重新运行命令，可能会成功下载之前失败的片段

### 3. 下载速度很慢

建议：
- 增加线程数（如 `-threads 20`）
- 检查网络连接速度
- 确认服务器是否限制了下载速度

### 4. 生成的 M3U8 文件无法播放

可能原因：
- 部分 TS 片段下载失败
- 本地播放器不支持 M3U8 格式
- 建议使用 VLC 或其他支持 M3U8 的播放器

## 💡 使用技巧

1. **批量下载**：使用任务文件管理多个下载任务
2. **网络优化**：根据网络状况调整线程数
3. **存储空间**：确保目标磁盘有足够的空间（TS 片段会占用较多空间）
4. **播放器选择**：使用支持 M3U8 格式的播放器（如 VLC）
5. **合并选项**：如需 MP4 格式，使用 FFmpeg 进行合并

## 🤝 贡献

欢迎提交 Issue 和 Pull Request 来改进这个项目！

## 📄 许可证

本项目采用 MIT 许可证 - 详见 [LICENSE](LICENSE) 文件

## 🙏 致谢

- 感谢 Go 语言提供的并发编程能力
- 感谢所有使用和支持本项目的用户

---

**快乐下载！** 🎉
