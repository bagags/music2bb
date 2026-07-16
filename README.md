# music2bb

将在线歌单转换为 Bilibili 收藏夹的 Go 项目，命令行程序名为 `music2bb`。它会自动识别歌单来源、解析歌曲、并发搜索并评分 Bilibili 视频，再将确认后的结果写入指定收藏夹。

本仓库最初 fork 自 [`gguage/music-to-bb`](https://github.com/gguage/music-to-bb)，此后以 `music2bb` 名称独立延续开发。

> [!IMPORTANT]
> music2bb 是独立开发的非官方开源项目，与 Apple、Apple Music、酷狗音乐、
> Bilibili（哔哩哔哩）或其他被引用的第三方无隶属关系，也未获得其认可或背书。
> 本项目仅用于个人互操作及迁移用户自行选择的歌单，不是内容获取工具。程序不
> 下载、上传、托管或解密任何音视频媒体内容；“转换”仅处理歌单和视频元数据及
> 标识符，并将用户选定的已有视频标识符加入其 Bilibili 收藏夹。完整声明见
> [`DISCLAIMER.md`](DISCLAIMER.md)。

## 功能

- 自动识别歌单来源，优先使用已注册的来源优化；HTTP 解析失败或不完整时自动通知并切换到受控 Chromium
- 保留酷狗直连 API、页面 JSON、分页、签名和歌曲清理优化
- 解析用户提供的 Apple Music 公开分享歌单页面及其中的公开元数据，无需 Apple 账号登录
- Bilibili 扫码登录、Cookie 持久化、WBI 签名和收藏夹管理
- 标题、歌手、音质、官方来源、热度和 UP 主六项归一化权重综合评分
- 默认 4 个受限并发 worker，保持输入与结果顺序
- 终端中自动启动覆盖登录、解析、匹配、审核、收藏夹、确认、写入和回执的全屏工作区
- 平衡式分阶段匹配、可解释审核原因、完全手动匹配和候选覆盖
- 可取消操作、稳定退出码、结构化部分失败结果
- 模块根包 `music2bb` 提供无终端依赖、可注入、适合未来 Go GUI 的公共 API

## 安装

从 [GitHub Releases](https://github.com/bagags/music2bb-go/releases) 下载与平台对应的压缩包，并使用随附的 `.sha256` 文件校验。压缩包包含已集成对应平台 Chromium 的单文件程序、GPLv3 许可证、非官方与无隶属关系声明、第三方软件声明、经来源差异审计并校验哈希的完整 Chromium credits、精确来源记录和对应源码信息。也可以直接安装当前源码：

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

当标准输入和标准输出都是终端且 `TERM` 不是 `dumb` 时，`convert` 默认进入全屏工作区。左侧始终按歌单顺序显示所有歌曲及文本状态，右侧显示来源元数据、审核原因、候选分数组成、UP 主、时长、BVID 和链接。小于 `80×20` 时改为单窗格，Tab 在前往下一首待审歌曲时同时切换窗格；小于 `40×12` 时保留退出与调整尺寸处理并显示尺寸提示。

审核快捷键：`←/→` 或 `h/l` 切换歌曲，`↑/↓` 或 `k/j` 切换候选，Enter 接受，Tab 前往下一首待审歌曲，`s` 手动搜索，`x` 跳过，`u` 清除选择，`c` 在所有待审歌曲已选择或跳过后继续，`?` 显示完整帮助，`q` 或 Ctrl-C 取消。写入期间取消会停止剩余操作并在退出全屏后保留一份包含已成功、失败和跳过数量的回执。

常用选项：

| 选项 | 默认值 | 说明 |
|---|---:|---|
| `--search-pages` | `3` | 每首歌搜索的页数 |
| `--top-k` | `3` | 为审核保留的有序候选数 |
| `--workers` | `4` | 并发匹配数量 |
| `--match-profile` | `standard` | `standard`（歌手导向）或 `classical`（作品标题导向） |
| `--favorite` | — | 收藏夹 ID 或完整名称 |
| `--yes` | `false` | 跳过最终写入确认 |
| `--browser` | `auto` | `auto`、`never` 或 `always` |
| `--manual-review` | `false` | 审核自动匹配候选 |
| `--manual` | `false` | 完全手动选择 |
| `--no-tui` | `false` | 强制使用无 ANSI、按歌单顺序输出的引导式文本界面 |
| `--no-qr-login` | `false` | 禁止自动发起扫码登录 |
| `--config-dir` | 系统目录 | 指定便携配置目录 |
| `--verbose`, `-v` | `false` | 输出详细进度 |

管道、CI、屏幕阅读器环境、`TERM=dumb` 或显式 `--no-tui` 会使用相同转换控制器下的文本界面。非交互式写入需要同时指定 `--favorite` 和 `--yes`；如果仍有无法自动解决的歌曲，则必须改在交互终端中逐首选择或跳过。
新建 Bilibili 收藏夹默认仅自己可见；如需公开收藏夹，请在 `favorites create` 命令中指定 `--public`。

## 匹配配置与审核原因

每个候选的标题、歌手、音质、官方来源、热度和 UP 主分量都在 0–100 范围内，再按当前配置计算加权平均。`standard` 是默认配置，权重依次为 40、25、10、10、10、5；它先执行包含歌手和已知别名的查询，标题匹配通过且歌手分为 100 时可以提前选择，否则继续聚合纯标题回退。最终选择要求标题分至少为 70、总分至少为 35，且比第二名领先至少 5 分。

`classical` 使用 55、10、10、10、10、5，更强调作品标题并允许不同演奏者或录音。曲名与候选标题包含同一个完整古典作品目录编号（例如 `BWV 1007`、`Hob. XVI:52`）时，标题分提升为 100；编号不同不会扣减原有相似度。它始终执行并聚合纯标题回退，不因歌手证据提前结束；最终标题分下限仍为 70，总分下限提高为 45，领先要求仍为 5 分。相近录音会以 `ambiguous` 进入审核。可通过 `--match-profile classical` 为一次转换启用，不做自动识别或持久化默认。内置目录符号表的来源、版本和解析规则见 [`docs/classical-catalogues.md`](docs/classical-catalogues.md)。

所有未自动解决的歌曲都必须选择或跳过。公共 `MatchResult.ReviewReason` 会给出 `no_candidates`、`search_failed`、`weak_title`、`artist_unverified` 或 `ambiguous`，全屏和文本界面都会显示对应说明。`--manual` 禁止自动选择；`--manual-review` 保留推荐但要求每首歌显式确认。

## 歌单解析与 Chromium 回退

程序根据原始 HTTP(S) URL 自动识别歌单来源，不需要也不提供 `--provider`。已识别来源会先运行已注册的优化；酷狗优化保留直连 API、页面数据和既有解析顺序，Apple Music 优化读取用户提供的公开分享歌单页面中的公开元数据，按页面顺序解析曲名、艺人、专辑、时长和声明总数，无需 Apple 账号登录。未知来源或没有歌单提取优化的来源，在策略允许时直接使用通用 Chromium 提取。只有来源优化失败、结果为空或少于页面声明总数时才触发浏览器回退；合并时来源优化结果优先，并保留可用的部分歌单。

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

浏览器版本、源码提交、快照修订、对象 generation、发布时间、归档大小和各平台 SHA-256 固定在程序内，也记录在 [`CHROMIUM_PROVENANCE.md`](CHROMIUM_PROVENANCE.md) 中。Chromium 各平台构建异步发布，所以当前记录中 macOS/Linux 已是 `152.0.7951.0`，Windows 的最新可用构建仍是 `152.0.7950.0`，而且每个平台可能对应相邻但不同的源码提交。无论归档来自发布版内置数据还是自动下载，只有 SHA-256 校验通过后才会解压；运行时只接受已记录并重新校验过的可执行文件。`browser status` 会显示准确版本、修订和完整提交哈希。显式执行 `browser install` 时仍会在交互终端确认可能发生的下载。

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

custom := music2bb.MatchWeights{Title: 6, Artist: 2, Quality: 1, Official: 1}
matches, err := engine.Match(ctx, songs, music2bb.MatchOptions{
    Profile: music2bb.MatchProfileClassical,
    Weights: &custom, // 相对值会被复制并按正数总和归一化
}, observer)
```

模块根包 `music2bb` 暴露上下文感知的登录、解析、匹配、搜索、收藏夹和浏览器操作，以及序列化观察者、类型化错误、审核原因和测试依赖注入。`StandardMatchWeights`、`ClassicalMatchWeights` 返回内置预设；`SearchCandidatesWithOptions` 让手动搜索沿用同一配置。自定义权重接受任意非负有限相对值，但至少一项必须为正；无效配置会在远端请求前返回 `ErrorInvalidInput`。非公开站点协议保留在 `internal` 包中。项目的包职责和依赖方向见 [`docs/architecture.md`](docs/architecture.md)。

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
MUSIC2BB_TEST_SEARCH_QUERY='贝多芬 第五交响曲' \
MUSIC2BB_TEST_COOKIE_FILE='/path/to/bilibili.json' \
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

CI 运行单元、fixture、race、vet、标签编译、集成目标平台 Chromium 的平台构建，以及发布平台的真实浏览器安装、启动和受控提取。CI 还会校验完整 Chromium credits 的固定哈希、项目数量和必需许可文本，并拒绝 Chromium snapshot 自带的示例 credits。`v*` 标签会发布包含内置 Chromium、许可证、第三方软件声明、完整 Chromium credits、精确来源记录和对应源码信息的版本化压缩包及其 SHA-256 文件。

## 许可证

Copyright (C) 2026 bagags and music2bb contributors.

This project contains code derived from earlier work by gguage and
continues development independently under the GPL-3.0-only license.

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
项目的非官方身份、无隶属关系、音视频媒体处理边界、商标归属和用户责任声明见 [`DISCLAIMER.md`](DISCLAIMER.md)。
运行 `music2bb license` 可在终端查看项目版权、无担保声明、许可证和源码地址。
