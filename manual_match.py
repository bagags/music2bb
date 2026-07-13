from __future__ import annotations

from typing import Optional

from rich.console import Console
from rich.table import Table
from rich.prompt import IntPrompt, Prompt, Confirm

from loguru import logger

from bilibili import BilibiliClient
from matcher import BilibiliMatcher
from models import KugouSong, BilibiliVideo, MatchResult

console = Console()


def manual_match_song(
    client: BilibiliClient,
    matcher: BilibiliMatcher,
    song: KugouSong,
    top_n: int = 10,
) -> Optional[MatchResult]:
    console.print(f"\n[bold cyan]手动匹配: {song.name} - {song.artist}[/bold cyan]")

    keyword = Prompt.ask(
        "搜索关键词",
        default=song.search_keyword,
    )

    console.print(f"[dim]正在搜索 '{keyword}' ...[/dim]")
    videos = client.search_videos(keyword, page=1, page_size=top_n)

    if not videos:
        console.print("[bold red]没有搜索结果[/bold red]")

        custom_bvid = Prompt.ask(
            "手动输入BV号（留空跳过）",
            default="",
        )
        if custom_bvid.strip():
            bvid = custom_bvid.strip()
            if bvid.startswith("BV"):
                detail = client.get_video_detail(bvid)
                if detail:
                    return MatchResult(
                        song=song,
                        video=detail,
                        score=999.0,
                        matched=True,
                        manual_override=True,
                    )
                else:
                    console.print("[bold red]无法获取视频信息[/bold red]")
        return None

    results = matcher.match(song, videos, top_k=len(videos))

    table = Table(title=f"搜索结果: {keyword}", show_lines=True)
    table.add_column("序号", style="cyan", width=4)
    table.add_column("标题", style="white", width=45)
    table.add_column("UP主", style="blue", width=15)
    table.add_column("播放", style="green", width=10)
    table.add_column("收藏", style="yellow", width=8)
    table.add_column("综合分", style="magenta", width=8)

    for i, r in enumerate(results, 1):
        v = r.video
        play = f"{v.play_count:,}" if v.play_count else "-"
        fav = f"{v.favorite_count:,}" if v.favorite_count else "-"
        table.add_row(
            str(i),
            v.title[:45] if v else "",
            v.uploader[:15] if v else "",
            play,
            fav,
            f"{r.score:.1f}",
        )

    table.add_row("0", "[dim]跳过此歌曲[/dim]", "", "", "", "")
    table.add_row("-1", "[dim]修改关键词重新搜索[/dim]", "", "", "", "")
    table.add_row("-2", "[dim]手动输入BV号[/dim]", "", "", "", "")

    console.print(table)

    while True:
        choice = IntPrompt.ask("选择序号", default=1)

        if choice == 0:
            return None
        elif choice == -1:
            return manual_match_song(client, matcher, song, top_n)
        elif choice == -2:
            custom_bvid = Prompt.ask("输入BV号")
            if custom_bvid.strip().startswith("BV"):
                detail = client.get_video_detail(custom_bvid.strip())
                if detail:
                    return MatchResult(
                        song=song,
                        video=detail,
                        score=999.0,
                        matched=True,
                        manual_override=True,
                    )
                else:
                    console.print("[bold red]无法获取视频信息[/bold red]")
            continue
        elif 1 <= choice <= len(results):
            selected = results[choice - 1]
            selected.manual_override = True
            selected.matched = True
            return selected
        else:
            console.print("[red]无效选择[/red]")


def interactive_review(
    client: BilibiliClient,
    matcher: BilibiliMatcher,
    results: list[MatchResult],
) -> list[MatchResult]:
    console.print("\n[bold cyan]=== 手动匹配模式 ===[/bold cyan]")
    console.print("[dim]你可以逐首检查匹配结果，替换不满意的选择[/dim]\n")

    updated: list[MatchResult] = []

    for i, r in enumerate(results):
        if r.matched and r.video:
            console.print(
                f"[{i+1}/{len(results)}] "
                f"[green]已匹配[/green] {r.song.name} - {r.song.artist} "
                f"→ {r.video.title}"
            )
            if Confirm.ask("替换此匹配?", default=False):
                new_match = manual_match_song(client, matcher, r.song)
                updated.append(new_match if new_match else r)
            else:
                updated.append(r)
        else:
            console.print(
                f"[{i+1}/{len(results)}] "
                f"[red]未匹配[/red] {r.song.name} - {r.song.artist}"
            )
            if Confirm.ask("手动搜索匹配?", default=True):
                new_match = manual_match_song(client, matcher, r.song)
                updated.append(new_match if new_match else r)
            else:
                updated.append(r)

    return updated
