from __future__ import annotations

import argparse
import sys


def main():
    parser = argparse.ArgumentParser(
        description="酷狗歌单 → Bilibili收藏夹 自动转换工具",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
示例:
  python main.py gui                                    # 启动GUI界面
  python main.py cli "https://m.kugou.com/share/zlist.html?id=12345"
  python main.py cli "https://m.kugou.com/share/zlist.html?id=12345" --qr-login --manual-review
  python main.py cli "https://m.kugou.com/share/zlist.html?id=12345" --search-pages 2 --verbose
        """,
    )
    subparsers = parser.add_subparsers(dest="mode", help="运行模式")

    gui_parser = subparsers.add_parser("gui", help="启动GUI界面")

    cli_parser = subparsers.add_parser("cli", help="命令行模式")
    cli_parser.add_argument("url", help="酷狗音乐歌单链接")
    cli_parser.add_argument("--search-pages", type=int, default=3, help="每首歌曲搜索的页数（默认3，每页20条）")
    cli_parser.add_argument("--top-k", type=int, default=3, help="每首歌曲保留的候选数量（默认3）")
    cli_parser.add_argument("--verbose", "-v", action="store_true", help="显示详细日志")
    cli_parser.add_argument("--no-qr-login", action="store_true", help="禁用扫码登录")
    cli_parser.add_argument("--manual-review", action="store_true", help="自动匹配后手动审核")
    cli_parser.add_argument("--manual", action="store_true", help="完全手动模式，跳过自动匹配直接手动选择")

    args = parser.parse_args()

    if args.mode == "gui":
        from gui import run_gui
        run_gui()
    elif args.mode == "cli":
        from core import run
        run(
            kugou_url=args.url,
            top_k=args.top_k,
            search_pages=args.search_pages,
            verbose=args.verbose,
            use_qr=not args.no_qr_login,
            manual_review=args.manual_review,
            manual_only=args.manual,
        )
    else:
        parser.print_help()
        print("\n提示: 使用 'python main.py gui' 启动GUI，或 'python main.py cli <链接>' 使用命令行")


if __name__ == "__main__":
    main()
