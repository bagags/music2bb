# music2bb

将在线歌单转换为 Bilibili 收藏夹的 Go 项目，命令行程序名为 `music2bb`。它会自动识别歌单来源、解析歌曲、并发搜索并评分 Bilibili 视频，再将确认后的结果写入指定收藏夹。

## 功能

- 自动识别歌单来源，优先使用已注册的来源优化；HTTP 解析失败或不完整时自动通知并切换到受控 Chromium
- 保留酷狗直连 API、页面 JSON、分页、签名和歌曲清理优化
- 使用 Apple Music 公开分享页的服务端序列化数据直接解析歌单，无需账号或 API 凭据
- Bilibili 扫码登录、Cookie 持久化、WBI 签名和收藏夹管理
- 关键词、音质、官方来源、热度和 UP 主权重综合评分
- 默认 4 个受限并发 worker，保持输入与结果顺序
- 自动匹配、候选审核、完全手动匹配和 BV 号覆盖
- 可取消操作、稳定退出码、结构化部分失败结果
- 模块根包 `music2bb` 提供无终端依赖、可注入、适合未来 Go GUI 的公共 API

## 安装

从 [GitHub Releases](https://github.com/bagags/music2bb-go/releases) 下载与平台对应的压缩包，并使用随附的 `.sha256` 文件校验。压缩包包含已集成对应平台 Chromium 的单文件程序、GPLv3 许可证、第三方软件声明和对应源码信息。也可以直接安装当前源码：

```bash
go install github.com/bagags/music2bb-go/cmd/music2bb@latest
```

本地构建：

```bash
git clone https://github.com/bagags/music2bb-go.git music2bb
cd music2bb
go build -trimpath -o music2bb ./cmd/music2bb
```

支持的发布目标：macOS ARM64、macOS AMD64、Windows AMD64、Windows ARM64。

## 使用

```text
music2bb convert <playlist-url> [options]
music2bb login
music2bb favorites list
music2bb favorites create <name> [--intro TEXT] [--public]
music2bb browser install|status|clear
music2bb version
music2bb license
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
新建 Bilibili 收藏夹默认仅自己可见；如需公开收藏夹，请在 `favorites create` 命令中指定 `--public`。

## 歌单解析与 Chromium 回退

程序根据原始 HTTP(S) URL 自动识别歌单来源，不需要也不提供 `--provider`。已识别来源会先运行已注册的优化；酷狗优化保留直连 API、页面数据和既有解析顺序，Apple Music 优化读取公开分享页中的 `serialized-server-data`，按页面顺序解析曲名、艺人、专辑、时长和声明总数，不需要 Apple API 凭据。未知来源或没有歌单提取优化的来源，在策略允许时直接使用通用 Chromium 提取。只有来源优化失败、结果为空或少于页面声明总数时才触发浏览器回退；合并时来源优化结果优先，并保留可用的部分歌单。

| `--browser` | 处理方式 |
|---|---|
| `never` | 只运行来源优化；未知或无对应优化的来源返回提取错误，不启动或安装 Chromium |
| `auto` | 先运行来源优化；结果为空或不完整时自动准备并启动已注入、已安装或发布版内置的 Chromium，同时输出切换通知 |
| `always` | 预先确保已注入、已安装或发布版内置的 Chromium 可用，仍先运行来源优化，并仅在结果为空或不完整时启动浏览器 |

正式发布构建把目标平台的固定 Chromium 归档嵌入程序。HTTP 解析失败或结果不完整时，后端会自动校验并解压该归档、输出切换通知，然后用 Chromium 重试；整个自动回退过程不会弹出确认提示。普通 `go build` 或 `go install` 构建不携带该大体积归档，CLI 会在首次需要回退时通知用户并自动下载、校验、安装后重试，同样不询问确认。`--browser never` 始终禁止这些行为。

```bash
music2bb browser status
music2bb browser install
music2bb browser clear
```

浏览器版本和各平台 SHA-256 固定在程序内。无论归档来自发布版内置数据还是自动下载，只有 SHA-256 校验通过后才会解压；运行时只接受已记录并重新校验过的可执行文件。显式执行 `browser install` 时仍会在交互终端确认可能发生的下载。

## 配置与迁移

默认配置目录：

- macOS：`~/Library/Application Support/music2bb`
- Windows：`%AppData%\music2bb`

首次浏览器回退后，解压的浏览器文件位于对应的系统缓存目录，不在配置目录中；发布二进制还包含用于首次安装的压缩归档。

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
import music2bb "github.com/bagags/music2bb-go"

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
MUSIC2BB_TEST_APPLE_MUSIC_URL='<apple-music-playlist-url>' \
MUSIC2BB_TEST_BVID='BV1xx411c7mD' \
go test -count=1 -tags=live ./internal/kugou ./internal/applemusic ./internal/bilibili
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

CI 运行单元、fixture、race、vet、标签编译、集成目标平台 Chromium 的平台构建，以及 macOS ARM64、Windows AMD64 和 Windows ARM64 的真实浏览器安装、启动和受控提取。`v*` 标签会发布包含内置 Chromium、许可证、第三方软件声明和对应源码信息的版本化压缩包及其 SHA-256 文件。

## 许可证

Copyright (C) 2026 Chaoyi Liu, bagags, and music2bb contributors.

music2bb is free software: you can redistribute it and/or modify it under the
terms of the GNU General Public License as published by the Free Software
Foundation, version 3 of the License. This project is licensed under
`GPL-3.0-only`; see [`LICENSE.md`](LICENSE.md) for the complete terms.

music2bb is distributed in the hope that it will be useful, but WITHOUT ANY
WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR
A PARTICULAR PURPOSE. See the GNU General Public License for more details.

You should have received a copy of the GNU General Public License along with
music2bb. If not, see <https://www.gnu.org/licenses/>.

发布包所含依赖项及内置 Chromium 的版权与许可证声明见 [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md)。
运行 `music2bb license` 可在终端查看项目版权、无担保声明、许可证和源码地址。
