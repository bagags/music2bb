from __future__ import annotations

import threading
import queue
import tkinter as tk
from tkinter import filedialog, messagebox
from typing import Optional, Callable

import customtkinter as ctk
from PIL import Image, ImageTk
from loguru import logger

from models import KugouSong, BilibiliVideo, MatchResult, BilibiliFavorite
from kugou import KugouScraper


ctk.set_appearance_mode("dark")
ctk.set_default_color_theme("blue")


class Kg2bbApp(ctk.CTk):
    def __init__(self):
        super().__init__()

        self.title("酷狗歌单 → Bilibili收藏夹")
        self.geometry("1100x750")
        self.minsize(900, 600)

        self._songs: list[KugouSong] = []
        self._results: list[MatchResult] = []
        self._favorites: list[BilibiliFavorite] = []
        self._selected_fav: Optional[BilibiliFavorite] = None
        self._logged_in: bool = False
        self._client = None
        self._scraper = None
        self._matcher = None
        self._task_queue: queue.Queue = queue.Queue()
        self._qr_photo: Optional[ImageTk.PhotoImage] = None

        self._build_ui()
        self._poll_task_queue()

    def _build_ui(self):
        self.grid_columnconfigure(0, weight=1)
        self.grid_rowconfigure(1, weight=1)

        self._build_top_bar()
        self._build_main_area()
        self._build_bottom_bar()

    def _build_top_bar(self):
        top = ctk.CTkFrame(self, height=50)
        top.grid(row=0, column=0, sticky="ew", padx=5, pady=(5, 0))
        top.grid_columnconfigure(1, weight=1)

        ctk.CTkLabel(top, text="酷狗歌单链接:", font=ctk.CTkFont(size=13)).grid(
            row=0, column=0, padx=(10, 5), pady=10
        )

        self.url_entry = ctk.CTkEntry(top, placeholder_text="粘贴酷狗概念版歌单链接...", width=400)
        self.url_entry.grid(row=0, column=1, padx=5, pady=10, sticky="ew")

        self.btn_parse = ctk.CTkButton(top, text="解析歌单", command=self._on_parse, width=100)
        self.btn_parse.grid(row=0, column=2, padx=5, pady=10)

        self.btn_login = ctk.CTkButton(top, text="扫码登录B站", command=self._on_qr_login, width=120, fg_color="#00a1d6", hover_color="#00b5e5")
        self.btn_login.grid(row=0, column=3, padx=5, pady=10)

        self.login_status = ctk.CTkLabel(top, text="未登录", text_color="gray", font=ctk.CTkFont(size=12))
        self.login_status.grid(row=0, column=4, padx=10, pady=10)

    def _build_main_area(self):
        main = ctk.CTkFrame(self)
        main.grid(row=1, column=0, sticky="nsew", padx=5, pady=5)
        main.grid_columnconfigure(0, weight=1)
        main.grid_columnconfigure(1, weight=0)
        main.grid_columnconfigure(2, weight=2)
        main.grid_rowconfigure(0, weight=1)

        self._build_song_list(main)
        self._build_match_detail(main)
        self._build_right_panel(main)

    def _build_song_list(self, parent):
        frame = ctk.CTkFrame(parent)
        frame.grid(row=0, column=0, sticky="nsew", padx=(0, 3), pady=0)
        frame.grid_rowconfigure(1, weight=1)
        frame.grid_columnconfigure(0, weight=1)

        ctk.CTkLabel(frame, text="歌曲列表", font=ctk.CTkFont(size=14, weight="bold")).grid(
            row=0, column=0, padx=10, pady=(10, 5), sticky="w"
        )

        self.song_list = ctk.CTkScrollableFrame(frame)
        self.song_list.grid(row=1, column=0, sticky="nsew", padx=5, pady=5)

        self.song_items: list[dict] = []

        btn_frame = ctk.CTkFrame(frame, fg_color="transparent")
        btn_frame.grid(row=2, column=0, padx=5, pady=5, sticky="ew")

        self.btn_auto_match = ctk.CTkButton(btn_frame, text="自动匹配全部", command=self._on_auto_match, width=130)
        self.btn_auto_match.pack(side="left", padx=3)

        self.btn_manual_match = ctk.CTkButton(btn_frame, text="手动匹配选中", command=self._on_manual_match, width=130, fg_color="#f39c12", hover_color="#e67e22")
        self.btn_manual_match.pack(side="left", padx=3)

    def _build_match_detail(self, parent):
        frame = ctk.CTkFrame(parent, width=220)
        frame.grid(row=0, column=1, sticky="nsew", padx=3, pady=0)
        frame.grid_rowconfigure(1, weight=1)
        frame.grid_columnconfigure(0, weight=1)

        ctk.CTkLabel(frame, text="匹配详情", font=ctk.CTkFont(size=14, weight="bold")).grid(
            row=0, column=0, padx=10, pady=(10, 5), sticky="w"
        )

        self.detail_frame = ctk.CTkScrollableFrame(frame, width=200)
        self.detail_frame.grid(row=1, column=0, sticky="nsew", padx=5, pady=5)

        self.detail_labels: dict[str, ctk.CTkLabel] = {}

    def _build_right_panel(self, parent):
        frame = ctk.CTkFrame(parent)
        frame.grid(row=0, column=2, sticky="nsew", padx=(3, 0), pady=0)
        frame.grid_rowconfigure(1, weight=1)
        frame.grid_columnconfigure(0, weight=1)

        header = ctk.CTkFrame(frame, fg_color="transparent")
        header.grid(row=0, column=0, padx=10, pady=(10, 5), sticky="ew")
        header.grid_columnconfigure(0, weight=1)

        ctk.CTkLabel(header, text="匹配结果", font=ctk.CTkFont(size=14, weight="bold")).grid(
            row=0, column=0, sticky="w"
        )

        self.match_stats = ctk.CTkLabel(header, text="", text_color="gray", font=ctk.CTkFont(size=11))
        self.match_stats.grid(row=0, column=1, sticky="e", padx=5)

        self.result_tree = ctk.CTkScrollableFrame(frame)
        self.result_tree.grid(row=1, column=0, sticky="nsew", padx=5, pady=5)

        self.result_rows: list[dict] = []

        bottom = ctk.CTkFrame(frame, fg_color="transparent")
        bottom.grid(row=2, column=0, padx=10, pady=10, sticky="ew")
        bottom.grid_columnconfigure(0, weight=1)

        ctk.CTkLabel(bottom, text="目标收藏夹:", font=ctk.CTkFont(size=12)).grid(
            row=0, column=0, sticky="w", padx=(0, 5)
        )

        self.fav_combo = ctk.CTkComboBox(bottom, values=["请先登录"], width=200, state="disabled")
        self.fav_combo.grid(row=0, column=1, padx=5)

        self.btn_refresh_fav = ctk.CTkButton(bottom, text="刷新", command=self._on_refresh_fav, width=60)
        self.btn_refresh_fav.grid(row=0, column=2, padx=5)

        self.btn_new_fav = ctk.CTkButton(bottom, text="新建收藏夹", command=self._on_create_fav, width=90, fg_color="#27ae60", hover_color="#219a52")
        self.btn_new_fav.grid(row=0, column=3, padx=5)

        self.btn_add_fav = ctk.CTkButton(
            bottom, text="添加到收藏夹", command=self._on_add_to_fav,
            width=130, fg_color="#e74c3c", hover_color="#c0392b"
        )
        self.btn_add_fav.grid(row=0, column=4, padx=5)

    def _build_bottom_bar(self):
        bottom = ctk.CTkFrame(self, height=40)
        bottom.grid(row=2, column=0, sticky="ew", padx=5, pady=(0, 5))
        bottom.grid_columnconfigure(0, weight=1)

        self.progress = ctk.CTkProgressBar(bottom, width=400)
        self.progress.grid(row=0, column=0, padx=10, pady=8, sticky="w")
        self.progress.set(0)

        self.status_label = ctk.CTkLabel(bottom, text="就绪", text_color="gray", font=ctk.CTkFont(size=12))
        self.status_label.grid(row=0, column=1, padx=10, pady=8, sticky="w")

        self.log_text = ctk.CTkTextbox(bottom, height=30, font=ctk.CTkFont(size=10), state="disabled")
        self.log_text.grid(row=0, column=2, padx=10, pady=8, sticky="ew")
        bottom.grid_columnconfigure(2, weight=1)

        self._setup_log_redirect()

    def _setup_log_redirect(self):
        def sink(message):
            record = message.record
            text = record["message"]
            level = record["level"].name
            self._task_queue.put(("log", f"[{level}] {text}"))

        logger.add(sink, level="INFO")

    def _poll_task_queue(self):
        while True:
            try:
                task = self._task_queue.get_nowait()
            except queue.Empty:
                break

            cmd = task[0]
            if cmd == "log":
                self._append_log(task[1])
            elif cmd == "status":
                self.status_label.configure(text=task[1])
            elif cmd == "progress":
                self.progress.set(task[1])
            elif cmd == "songs_loaded":
                self._refresh_song_list()
            elif cmd == "login_ok":
                self._on_login_success(task[1])
            elif cmd == "login_fail":
                self._on_login_fail(task[1])
            elif cmd == "qr_image":
                self._show_qr_dialog(task[1])
            elif cmd == "match_done":
                self._refresh_result_list()
            elif cmd == "fav_list":
                self._update_fav_combo(task[1])
            elif cmd == "add_result":
                self._show_add_result(task[1])
            elif cmd == "manual_search":
                self._show_manual_match_dialog(task[1], task[2])
            elif cmd == "single_match_done":
                self._on_single_match_done(task[1], task[2])

        self.after(100, self._poll_task_queue)

    def _append_log(self, text: str):
        self.log_text.configure(state="normal")
        self.log_text.insert("end", text + "\n")
        self.log_text.see("end")
        self.log_text.configure(state="disabled")

    def _set_status(self, text: str):
        self._task_queue.put(("status", text))

    def _set_progress(self, value: float):
        self._task_queue.put(("progress", value))

    def _ensure_backend(self) -> bool:
        if self._client is None:
            from bilibili import BilibiliClient
            from kugou import KugouScraper
            from matcher import BilibiliMatcher

            self._client = BilibiliClient()
            self._scraper = KugouScraper()
            self._matcher = BilibiliMatcher()

            if self._client.load_cookies("bilibili"):
                if self._client.is_logged_in():
                    self._logged_in = True
                    self._task_queue.put(("login_ok", "Cookie登录成功"))
                    self._load_fav_list()

        return self._client is not None

    def _on_parse(self):
        url = self.url_entry.get().strip()
        if not url:
            messagebox.showwarning("提示", "请输入酷狗歌单链接")
            return

        self._ensure_backend()
        self.btn_parse.configure(state="disabled")
        self._set_status("正在解析歌单...")

        def worker():
            songs = []
            try:
                scraper_no_pw = KugouScraper()
                songs = scraper_no_pw.scrape_playlist(url)
                scraper_no_pw._http.close()
            except Exception as e:
                logger.debug(f"HTTP scrape failed: {e}")

            if not songs:
                try:
                    from playwright.sync_api import sync_playwright
                    self._set_status("使用浏览器解析歌单...")
                    pw = sync_playwright().start()
                    browser = pw.chromium.launch(headless=True)
                    ctx = browser.new_context(
                        viewport={"width": 390, "height": 844},
                        user_agent="Mozilla/5.0 (Linux; Android 12; Pixel 6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/111.0.0.0 Mobile Safari/537.36",
                    )
                    pw_page = ctx.new_page()
                    scraper_pw = KugouScraper(page=pw_page)
                    songs = scraper_pw.scrape_playlist(url)
                    ctx.close()
                    browser.close()
                    pw.stop()
                except Exception as e2:
                    logger.error(f"浏览器解析也失败: {e2}")

            self._songs = songs
            self._set_status(f"解析完成: {len(songs)} 首歌曲")
            self._task_queue.put(("songs_loaded", None))
            self.btn_parse.configure(state="normal")

        threading.Thread(target=worker, daemon=True).start()

    def _refresh_song_list(self):
        for widget in self.song_list.winfo_children():
            widget.destroy()
        self.song_items.clear()

        for i, song in enumerate(self._songs):
            row = ctk.CTkFrame(self.song_list, fg_color="transparent")
            row.pack(fill="x", pady=1, padx=2)

            var = tk.BooleanVar(value=False)
            cb = ctk.CTkCheckBox(row, text="", variable=var, width=20)
            cb.pack(side="left", padx=(2, 5))

            label = ctk.CTkLabel(
                row,
                text=f"{i+1}. {song.name} - {song.artist}",
                font=ctk.CTkFont(size=12),
                anchor="w",
            )
            label.pack(side="left", fill="x", expand=True)

            status = ctk.CTkLabel(row, text="", font=ctk.CTkFont(size=11), width=60, anchor="e")
            status.pack(side="right", padx=5)

            self.song_items.append({"var": var, "song": song, "status": status, "index": i})

    def _on_qr_login(self):
        self._ensure_backend()
        self.btn_login.configure(state="disabled")
        self._set_status("正在生成二维码...")

        def worker():
            try:
                def on_qr(img):
                    self._task_queue.put(("qr_image", img))

                success = self._client.qr_login(on_qr_image=on_qr)
                if success:
                    self._logged_in = True
                    resp = self._client._api_get(self._client.NAV_API)
                    uname = ""
                    if resp and resp.get("code") == 0:
                        uname = resp.get("data", {}).get("uname", "")
                    self._task_queue.put(("login_ok", f"已登录: {uname}"))
                    self._load_fav_list()
                else:
                    self._task_queue.put(("login_fail", "扫码登录失败或已超时"))
            except Exception as e:
                logger.error(f"QR login error: {e}")
                self._task_queue.put(("login_fail", str(e)))
            finally:
                self.btn_login.configure(state="normal")

        threading.Thread(target=worker, daemon=True).start()

    def _show_qr_dialog(self, img: Image.Image):
        dialog = ctk.CTkToplevel(self)
        dialog.title("扫码登录Bilibili")
        dialog.geometry("400x480")
        dialog.attributes("-topmost", True)
        dialog.grab_set()

        ctk.CTkLabel(dialog, text="请使用Bilibili APP扫描二维码", font=ctk.CTkFont(size=16, weight="bold")).pack(pady=(20, 10))

        img_resized = img.resize((300, 300), Image.LANCZOS)
        ctk_img = ctk.CTkImage(light_image=img_resized, dark_image=img_resized, size=(300, 300))
        label = ctk.CTkLabel(dialog, image=ctk_img, text="")
        label.image = ctk_img
        label.pack(pady=10)

        ctk.CTkLabel(dialog, text="扫描后请在手机上确认登录", text_color="gray", font=ctk.CTkFont(size=12)).pack(pady=5)

        self._qr_dialog = dialog

    def _on_login_success(self, msg: str):
        self.login_status.configure(text=msg, text_color="#2ecc71")
        self._set_status(msg)
        if hasattr(self, "_qr_dialog") and self._qr_dialog:
            try:
                self._qr_dialog.destroy()
            except Exception:
                pass

    def _on_login_fail(self, msg: str):
        self.login_status.configure(text="登录失败", text_color="#e74c3c")
        self._set_status(f"登录失败: {msg}")
        if hasattr(self, "_qr_dialog") and self._qr_dialog:
            try:
                self._qr_dialog.destroy()
            except Exception:
                pass

    def _load_fav_list(self):
        def worker():
            try:
                favs = self._client.get_favorite_lists()
                self._favorites = favs
                self._task_queue.put(("fav_list", favs))
            except Exception as e:
                logger.error(f"Load favorites failed: {e}")

        threading.Thread(target=worker, daemon=True).start()

    def _on_refresh_fav(self):
        if not self._logged_in:
            messagebox.showwarning("提示", "请先登录Bilibili")
            return
        self._load_fav_list()

    def _on_create_fav(self):
        if not self._logged_in:
            messagebox.showwarning("提示", "请先登录Bilibili")
            return

        dialog = ctk.CTkToplevel(self)
        dialog.title("新建收藏夹")
        dialog.geometry("400x250")
        dialog.attributes("-topmost", True)
        dialog.grab_set()
        dialog.grid_columnconfigure(1, weight=1)

        ctk.CTkLabel(dialog, text="收藏夹名称:", font=ctk.CTkFont(size=13)).grid(
            row=0, column=0, padx=(15, 5), pady=(20, 5), sticky="w"
        )
        title_entry = ctk.CTkEntry(dialog, width=250, placeholder_text="输入收藏夹名称")
        title_entry.grid(row=0, column=1, padx=(5, 15), pady=(20, 5), sticky="ew")

        ctk.CTkLabel(dialog, text="简介(可选):", font=ctk.CTkFont(size=13)).grid(
            row=1, column=0, padx=(15, 5), pady=5, sticky="w"
        )
        intro_entry = ctk.CTkEntry(dialog, width=250, placeholder_text="输入简介")
        intro_entry.grid(row=1, column=1, padx=(5, 15), pady=5, sticky="ew")

        ctk.CTkLabel(dialog, text="隐私:", font=ctk.CTkFont(size=13)).grid(
            row=2, column=0, padx=(15, 5), pady=5, sticky="w"
        )
        privacy_var = tk.IntVar(value=0)
        privacy_frame = ctk.CTkFrame(dialog, fg_color="transparent")
        privacy_frame.grid(row=2, column=1, padx=(5, 15), pady=5, sticky="w")
        ctk.CTkRadioButton(privacy_frame, text="公开", variable=privacy_var, value=0).pack(side="left", padx=5)
        ctk.CTkRadioButton(privacy_frame, text="仅自己可见", variable=privacy_var, value=1).pack(side="left", padx=5)

        def on_create():
            title = title_entry.get().strip()
            if not title:
                messagebox.showwarning("提示", "请输入收藏夹名称", parent=dialog)
                return
            dialog.destroy()
            self._do_create_fav(title, intro_entry.get().strip(), privacy_var.get())

        btn_frame = ctk.CTkFrame(dialog, fg_color="transparent")
        btn_frame.grid(row=3, column=0, columnspan=2, pady=20)
        ctk.CTkButton(btn_frame, text="创建", command=on_create, fg_color="#27ae60", hover_color="#219a52", width=100).pack(side="left", padx=10)
        ctk.CTkButton(btn_frame, text="取消", command=dialog.destroy, fg_color="gray", width=80).pack(side="left", padx=10)

    def _do_create_fav(self, title: str, intro: str, privacy: int):
        self.btn_new_fav.configure(state="disabled")
        self._set_status(f"正在创建收藏夹「{title}」...")

        def worker():
            try:
                fav = self._client.create_favorite(title, intro, privacy)
                if fav:
                    self._set_status(f"收藏夹「{title}」创建成功")
                    self._load_fav_list()
                else:
                    self._set_status("创建收藏夹失败")
            except Exception as e:
                logger.error(f"Create favorite error: {e}")
                self._set_status(f"创建失败: {e}")
            finally:
                self.btn_new_fav.configure(state="normal")

        threading.Thread(target=worker, daemon=True).start()

    def _update_fav_combo(self, favs: list[BilibiliFavorite]):
        values = [f"{f.title} ({f.media_count}个内容)" for f in favs]
        if values:
            self.fav_combo.configure(values=values, state="normal")
            self.fav_combo.set(values[0])
        else:
            self.fav_combo.configure(values=["无收藏夹"], state="disabled")
            self.fav_combo.set("无收藏夹")

    def _on_auto_match(self):
        if not self._songs:
            messagebox.showwarning("提示", "请先解析歌单")
            return
        if not self._logged_in:
            messagebox.showwarning("提示", "请先登录Bilibili")
            return

        self.btn_auto_match.configure(state="disabled")
        self._results = [MatchResult(song=s) for s in self._songs]
        self._set_status("正在自动匹配...")

        def worker():
            total = len(self._songs)
            for i, song in enumerate(self._songs):
                self._set_status(f"匹配中 [{i+1}/{total}]: {song.search_keyword_full}")
                self._set_progress((i + 1) / total)

                try:
                    keyword = song.search_keyword_full
                    videos = []
                    for pg in range(1, 4):
                        page_results = self._client.search_videos(keyword, page=pg, page_size=20)
                        videos.extend(page_results)

                    if videos:
                        top = self._matcher.match(song, videos, top_k=1)
                        if top and top[0].matched:
                            self._results[i] = top[0]
                        else:
                            alt_keywords = song.get_all_search_keywords()
                            for alt_kw in alt_keywords:
                                if alt_kw == keyword:
                                    continue
                                alt_videos = []
                                for pg in range(1, 3):
                                    page_results = self._client.search_videos(alt_kw, page=pg, page_size=20)
                                    alt_videos.extend(page_results)
                                if alt_videos:
                                    top = self._matcher.match(song, alt_videos, top_k=1)
                                    if top and top[0].matched:
                                        self._results[i] = top[0]
                                        break
                except Exception as e:
                    logger.error(f"Match error for {song.name}: {e}")

            self._set_status("匹配完成")
            self._set_progress(0)
            self._task_queue.put(("match_done", None))
            self.btn_auto_match.configure(state="normal")

        threading.Thread(target=worker, daemon=True).start()

    def _on_manual_match(self):
        if not self._logged_in:
            messagebox.showwarning("提示", "请先登录Bilibili")
            return

        selected_indices = [
            item["index"] for item in self.song_items if item["var"].get()
        ]

        if not selected_indices:
            messagebox.showwarning("提示", "请先在左侧勾选要手动匹配的歌曲")
            return

        for idx in selected_indices:
            song = self._songs[idx]
            self._set_status(f"手动匹配: {song.search_keyword_full}")

            def worker(s=song, i=idx):
                try:
                    videos = self._client.search_videos(s.search_keyword_full, page=1, page_size=20)
                    self._task_queue.put(("manual_search", i, videos))
                except Exception as e:
                    logger.error(f"Manual search error: {e}")

            threading.Thread(target=worker, daemon=True).start()
            break

    def _show_manual_match_dialog(self, song_idx: int, videos: list[BilibiliVideo]):
        song = self._songs[song_idx]

        dialog = ctk.CTkToplevel(self)
        dialog.title(f"手动匹配: {song.name} - {song.artist}")
        dialog.geometry("700x500")
        dialog.attributes("-topmost", True)
        dialog.grab_set()

        dialog.grid_columnconfigure(0, weight=1)
        dialog.grid_rowconfigure(2, weight=1)

        search_frame = ctk.CTkFrame(dialog)
        search_frame.grid(row=0, column=0, sticky="ew", padx=10, pady=10)
        search_frame.grid_columnconfigure(1, weight=1)

        ctk.CTkLabel(search_frame, text="搜索:").grid(row=0, column=0, padx=5)
        search_entry = ctk.CTkEntry(search_frame, width=300)
        search_entry.insert(0, song.search_keyword_full)
        search_entry.grid(row=0, column=1, padx=5, sticky="ew")

        ctk.CTkLabel(dialog, text=f"为「{song.name} - {song.artist}」选择匹配视频:", font=ctk.CTkFont(size=13)).grid(
            row=1, column=0, padx=10, pady=5, sticky="w"
        )

        list_frame = ctk.CTkScrollableFrame(dialog)
        list_frame.grid(row=2, column=0, sticky="nsew", padx=10, pady=5)

        selected_bvid: list[Optional[str]] = [None]

        def make_select(bvid: str):
            def handler():
                selected_bvid[0] = bvid
            return handler

        for i, v in enumerate(videos):
            row = ctk.CTkFrame(list_frame, fg_color="transparent")
            row.pack(fill="x", pady=2, padx=2)

            play = f"{v.play_count:,}" if v.play_count else "-"
            fav = f"{v.favorite_count:,}" if v.favorite_count else "-"
            text = f"{i+1}. {v.title}  |  UP: {v.uploader}  |  播放:{play} 收藏:{fav}"

            rb = ctk.CTkRadioButton(
                row, text=text, variable=None, value=i,
                font=ctk.CTkFont(size=11),
            )
            rb.pack(fill="x", padx=5, pady=2)
            rb.configure(command=make_select(v.bvid))

        bv_frame = ctk.CTkFrame(dialog, fg_color="transparent")
        bv_frame.grid(row=3, column=0, padx=10, pady=5, sticky="ew")
        bv_frame.grid_columnconfigure(1, weight=1)

        ctk.CTkLabel(bv_frame, text="或输入BV号:").grid(row=0, column=0, padx=5)
        bv_entry = ctk.CTkEntry(bv_frame, width=200, placeholder_text="BVxxxxxxxxxx")
        bv_entry.grid(row=0, column=1, padx=5, sticky="w")

        def on_confirm():
            bvid = bv_entry.get().strip() or selected_bvid[0]
            if not bvid or not bvid.startswith("BV"):
                messagebox.showwarning("提示", "请选择视频或输入BV号")
                return

            def worker():
                try:
                    detail = self._client.get_video_detail(bvid)
                    if detail:
                        result = MatchResult(
                            song=song, video=detail, score=999.0,
                            matched=True, manual_override=True,
                        )
                        if song_idx < len(self._results):
                            self._results[song_idx] = result
                        else:
                            while len(self._results) <= song_idx:
                                self._results.append(MatchResult(song=self._songs[len(self._results)]))
                            self._results[song_idx] = result
                        self._task_queue.put(("single_match_done", song_idx, result))
                except Exception as e:
                    logger.error(f"Manual match error: {e}")

            dialog.destroy()
            threading.Thread(target=worker, daemon=True).start()

        def on_skip():
            dialog.destroy()

        btn_frame = ctk.CTkFrame(dialog, fg_color="transparent")
        btn_frame.grid(row=4, column=0, padx=10, pady=10)

        ctk.CTkButton(btn_frame, text="确认选择", command=on_confirm, fg_color="#2ecc71", hover_color="#27ae60").pack(side="left", padx=10)
        ctk.CTkButton(btn_frame, text="跳过", command=on_skip, fg_color="gray").pack(side="left", padx=10)

        def on_search():
            kw = search_entry.get().strip()
            if not kw:
                return

            def worker():
                try:
                    new_videos = self._client.search_videos(kw, page=1, page_size=20)
                    for widget in list_frame.winfo_children():
                        widget.destroy()
                    selected_bvid[0] = None

                    for i, v in enumerate(new_videos):
                        row = ctk.CTkFrame(list_frame, fg_color="transparent")
                        row.pack(fill="x", pady=2, padx=2)
                        play = f"{v.play_count:,}" if v.play_count else "-"
                        fav = f"{v.favorite_count:,}" if v.favorite_count else "-"
                        text = f"{i+1}. {v.title}  |  UP: {v.uploader}  |  播放:{play} 收藏:{fav}"
                        rb = ctk.CTkRadioButton(row, text=text, font=ctk.CTkFont(size=11))
                        rb.pack(fill="x", padx=5, pady=2)
                        rb.configure(command=make_select(v.bvid))
                except Exception as e:
                    logger.error(f"Search error: {e}")

            threading.Thread(target=worker, daemon=True).start()

        ctk.CTkButton(search_frame, text="搜索", command=on_search, width=60).grid(row=0, column=2, padx=5)

    def _on_single_match_done(self, idx: int, result: MatchResult):
        if idx < len(self.song_items):
            self.song_items[idx]["status"].configure(text="已匹配", text_color="#2ecc71")
        self._refresh_result_list()
        self._set_status(f"手动匹配完成: {result.song.name}")

    def _refresh_result_list(self):
        for widget in self.result_tree.winfo_children():
            widget.destroy()
        self.result_rows.clear()

        matched_count = sum(1 for r in self._results if r.matched)
        total = len(self._results)
        self.match_stats.configure(text=f"匹配: {matched_count}/{total}")

        for i, r in enumerate(self._results):
            row = ctk.CTkFrame(self.result_tree, fg_color="transparent")
            row.pack(fill="x", pady=1, padx=2)

            if r.matched and r.video:
                v = r.video
                icon = "[手]" if r.manual_override else "[自]"
                text = f"{icon} {r.song.name} → {v.title[:35]}"
                color = "#f39c12" if r.manual_override else "#2ecc71"
                tip = f"UP:{v.uploader} | 播放:{v.play_count:,} | 收藏:{v.favorite_count:,}"
            else:
                text = f"[×] {r.song.name} - {r.song.artist}"
                color = "#e74c3c"
                tip = "未匹配"

            label = ctk.CTkLabel(row, text=text, text_color=color, font=ctk.CTkFont(size=11), anchor="w")
            label.pack(fill="x", padx=5, pady=1)

            if tip:
                ctk.CTkLabel(row, text=tip, text_color="gray", font=ctk.CTkFont(size=10), anchor="w").pack(
                    fill="x", padx=20, pady=0
                )

        for i, item in enumerate(self.song_items):
            if i < len(self._results):
                r = self._results[i]
                if r.matched:
                    item["status"].configure(text="已匹配", text_color="#2ecc71")
                else:
                    item["status"].configure(text="未匹配", text_color="#e74c3c")

    def _on_add_to_fav(self):
        if not self._logged_in:
            messagebox.showwarning("提示", "请先登录Bilibili")
            return
        if not self._results:
            messagebox.showwarning("提示", "请先进行匹配")
            return

        fav_idx = self.fav_combo.get()
        if not self._favorites:
            messagebox.showwarning("提示", "请先刷新收藏夹列表")
            return

        fav = self._favorites[0]
        for f in self._favorites:
            if f.title in fav_idx:
                fav = f
                break

        matched = [r for r in self._results if r.matched and r.video]
        videos = [r.video for r in matched]  # 传入视频对象列表，包含aid

        if not videos:
            messagebox.showwarning("提示", "没有可添加的视频")
            return

        if not messagebox.askyesno("确认", f"将 {len(videos)} 个视频添加到「{fav.title}」?"):
            return

        self.btn_add_fav.configure(state="disabled")
        self._set_status("正在添加到收藏夹...")

        def worker():
            try:
                result = self._client.add_to_favorites(videos, fav.fid)
                self._task_queue.put(("add_result", result))
            except Exception as e:
                logger.error(f"Add to favorites error: {e}")
                self._set_status(f"添加失败: {e}")
            finally:
                self.btn_add_fav.configure(state="normal")

        threading.Thread(target=worker, daemon=True).start()

    def _show_add_result(self, result: dict):
        ok = len(result.get("success", []))
        fail = len(result.get("failed", []))
        self._set_status(f"添加完成: 成功{ok}, 失败{fail}")
        if fail > 0:
            msgs = "\n".join(f"{i['bvid']}: {i['reason']}" for i in result["failed"][:5])
            messagebox.showwarning("部分失败", f"成功: {ok}\n失败: {fail}\n\n{msgs}")
        else:
            messagebox.showinfo("完成", f"成功添加 {ok} 个视频到收藏夹")

    def destroy(self):
        if self._client:
            try:
                self._client.close()
            except Exception:
                pass
        super().destroy()


def run_gui():
    app = Kg2bbApp()
    app.mainloop()
