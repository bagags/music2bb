# music2bb

将在线歌单转换为 Bilibili 收藏夹的 Go 项目，命令行程序名为 `music2bb`。它会自动识别歌单来源、解析歌曲、并发搜索并评分 Bilibili 视频，再将确认后的结果写入指定收藏夹。

## 功能

- 自动识别歌单来源，优先使用已注册的来源优化，并以受控 Chromium 提供通用网页回退
- 保留酷狗直连 API、页面 JSON、分页、签名和歌曲清理优化
- Bilibili 扫码登录、Cookie 持久化、WBI 签名和收藏夹管理
- 关键词、音质、官方来源、热度和 UP 主权重综合评分
- 默认 4 个受限并发 worker，保持输入与结果顺序
- 自动匹配、候选审核、完全手动匹配和 BV 号覆盖
- 可取消操作、稳定退出码、结构化部分失败结果
- 模块根包 `music2bb` 提供无终端依赖、可注入、适合未来 Go GUI 的公共 API

## 安装

从 [GitHub Releases](https://github.com/gguage/music-to-bb/releases) 下载与平台对应的单文件程序，并使用随附的 `.sha256` 文件校验；或安装当前源码：

```bash
go install github.com/gguage/music-to-bb/cmd/music2bb@latest
```

本地构建：

```bash
git clone https://github.com/gguage/music-to-bb.git music2bb
cd music2bb
go build -trimpath -o music2bb ./cmd/music2bb
```

支持的发布目标：macOS ARM64、macOS AMD64、Windows AMD64、Windows ARM64。

## 使用

```text
music2bb convert <playlist-url> [options]
music2bb login
music2bb favorites list
music2bb favorites create <name> [--intro TEXT] [--private]
music2bb browser install|status|clear
music2bb version
```

首次登录：

```bash
music2bb login
```

自动转换到指定收藏夹（以下使用酷狗优化来源作为示例）：

```bash
music2bb convert 'https://m.kugou.com/share/zlist.html?specialid=3339907' \
  --favorite Music --yes
```

保留五个候选并逐首审核：

```bash
music2bb convert '<playlist-url>' --top-k 5 --manual-review
```

常用选项：

| 选项 | 默认值 | 说明 |
|---|---:|---|
| `--search-pages` | `3` | 每首歌搜索的页数 |
| `--top-k` | `3` | 为审核保留的有序候选数 |
| `--workers` | `4` | 并发匹配数量 |
| `--favorite` | — | 收藏夹 ID 或完整名称 |
| `--yes` | `false` | 跳过最终写入确认 |
| `--browser` | `auto` | `auto`、`never` 或 `always` |
| `--manual-review` | `false` | 审核自动匹配候选 |
| `--manual` | `false` | 完全手动选择 |
| `--no-qr-login` | `false` | 禁止自动发起扫码登录 |
| `--config-dir` | 系统目录 | 指定便携配置目录 |
| `--verbose`, `-v` | `false` | 输出详细进度 |

非交互式写入需要同时指定 `--favorite` 和 `--yes`。

## 歌单解析与 Chromium 回退

程序根据原始 HTTP(S) URL 自动识别歌单来源，不需要也不提供 `--provider`。已识别来源会先运行已注册的优化；当前酷狗优化保留直连 API、页面数据和既有解析顺序。未知来源或没有歌单提取优化的来源，在策略允许时直接使用通用 Chromium 提取。只有来源优化结果为空或少于页面声明总数时才触发浏览器回退；合并时来源优化结果优先，并保留可用的部分歌单。

| `--browser` | 处理方式 |
|---|---|
| `never` | 只运行来源优化；未知或无对应优化的来源返回提取错误，不启动或安装 Chromium |
| `auto` | 先运行来源优化，仅在需要时使用已注入或已安装的 Chromium；浏览器不可用或回退失败时保留可用的部分结果 |
| `always` | 要求 Chromium 已注入或通过校验安装，仍先运行来源优化，并仅在结果为空或不完整时启动浏览器 |

后端解析过程从不安装 Chromium，也不会弹出确认提示。CLI 在解析失败后负责查看浏览器状态、请求批准、安装并重试。浏览器不打包进程序；下载前会显示当前平台归档的实际近似大小，并要求明确批准。

```bash
music2bb browser status
music2bb browser install
music2bb browser clear
```

浏览器版本和各平台 SHA-256 固定在程序内。下载完成、校验通过后才会解压；运行时只接受已记录并重新校验过的可执行文件。

## 配置与迁移

默认配置目录：

- macOS：`~/Library/Application Support/music2bb`
- Windows：`%AppData%\music2bb`

浏览器文件位于对应的系统缓存目录，不在配置目录或发布二进制中。

可选覆盖文件：

| 文件 | 作用 |
|---|---|
| `b.txt` | 屏蔽关键词 |
| `w.txt` | 标题、简介和标签加权关键词 |
| `w-up.txt` | 精确匹配的 UP 主加权列表 |

程序内置默认列表。配置目录中的同名文件会覆盖内置值。首次运行会识别工作目录或可执行文件目录中的旧 `.cookies/bilibili.json`、`b.txt`、`w.txt` 和 `w-up.txt`，以原子写入方式复制到新目录；不会修改或删除旧文件。Cookie 文件使用仅所有者可读写权限。

## Go 后端

CLI 只通过公共包调用后端：

```go
import music2bb "github.com/gguage/music-to-bb"

engine, err := music2bb.New(music2bb.Config{})
if err != nil {
    return err
}
defer engine.Close()

songs, err := engine.ParsePlaylist(ctx, playlistURL, observer)
```

模块根包 `music2bb` 暴露上下文感知的登录、解析、匹配、搜索、收藏夹和浏览器操作，以及序列化观察者、类型化错误和测试依赖注入。非公开站点协议保留在 `internal` 包中。项目的包职责和依赖方向见 [`docs/architecture.md`](docs/architecture.md)。

## 测试

默认验证：

```bash
go test ./...
go test -race ./...
go vet ./...
```

只读线上 canary：

```bash
MUSIC2BB_TEST_KUGOU_URL='<playlist-url>' \
MUSIC2BB_TEST_BVID='BV1xx411c7mD' \
go test -count=1 -tags=live ./internal/kugou ./internal/bilibili
```

使用已下载的固定 Chromium 归档运行安装、启动和动态页面提取：

```bash
MUSIC2BB_TEST_BROWSER_ARCHIVE='/path/to/chromium.zip' \
go test -count=1 -tags=browser_install ./internal/browser \
  -run TestPinnedArchiveInstallLaunchAndExtraction -v
```

认证 canary 会创建临时私有收藏夹、添加并验证一个视频，然后移除资源并删除收藏夹。它会产生短暂的远端写入，因此需要双重显式启用：

```bash
MUSIC2BB_RUN_AUTH_CANARY=1 \
MUSIC2BB_TEST_COOKIE_FILE='/path/to/bilibili.json' \
MUSIC2BB_TEST_BVID='BV1xx411c7mD' \
go test -count=1 -tags=authenticated ./internal/bilibili \
  -run TestAuthenticatedFavoriteLifecycleCanary -v
```

CI 运行单元、fixture、race、vet、标签编译、平台构建以及 macOS ARM64、Windows AMD64 和 Windows ARM64 的真实浏览器安装、启动和受控提取。`v*` 标签会发布精简的版本化二进制和 SHA-256 文件。
