import json
import logging
import os
import re
import shutil
import subprocess
from typing import Any, Dict, List, Optional
from config import CONFIG

logger = logging.getLogger("autoclip.fetcher")

EPISODE_REGEX = re.compile(r"(?:b[öo]l[üu]m|ep(?:isode)?)\s*(\d+)|(\d+)\.\s*b[öo]l[üu]m", re.IGNORECASE)


class MediaFetcher:
    """
    Hedef medya kaynaklarını (resmi YouTube kanalları / oynatma listeleri / RSS)
    tarayarak yeni videoları tespit eder ve yt-dlp ile 1080p MP4 olarak indirir.
    """

    def __init__(self, media_root: str = CONFIG.media_root):
        self.media_root = media_root
        self.raw_dir = os.path.join(self.media_root, "raw")
        os.makedirs(self.raw_dir, exist_ok=True)
        self.has_aria2c = shutil.which("aria2c") is not None

    def extract_latest_entries(self, source_url: str, max_entries: int = 5) -> List[Dict[str, Any]]:
        """
        Kaynak URL'deki son videoların мета verilerini (indirmeden) çeker.
        """
        cmd = [
            "yt-dlp",
            "--flat-playlist",
            "--dump-single-json",
            "--playlist-end",
            str(max_entries),
            source_url,
        ]
        try:
            proc = subprocess.run(
                cmd,
                capture_output=True,
                text=True,
                check=True,
                timeout=120,
            )
            data = json.loads(proc.stdout)
        except Exception as exc:
            logger.error("Kaynak taranırken hata (%s): %s", source_url, exc)
            return []

        entries = data.get("entries")
        if entries is None and data.get("id"):
            entries = [data]

        results: List[Dict[str, Any]] = []
        for idx, entry in enumerate(entries or [], start=1):
            vid_id = entry.get("id")
            title = entry.get("title") or f"Episode {idx}"
            url = entry.get("url") or entry.get("webpage_url") or source_url
            if vid_id and not url.startswith("http"):
                url = f"https://www.youtube.com/watch?v={vid_id}"

            ep_num = self.parse_episode_number(title, default_num=idx)
            if vid_id:
                results.append(
                    {
                        "video_id": str(vid_id),
                        "title": title,
                        "episode_number": ep_num,
                        "video_url": url,
                    }
                )
        return results

    @staticmethod
    def parse_episode_number(title: str, default_num: int = 1) -> int:
        match = EPISODE_REGEX.search(title)
        if match:
            num_str = match.group(1) or match.group(2)
            if num_str and num_str.isdigit():
                return int(num_str)
        digits = re.findall(r"\b(\d{1,4})\b", title)
        if digits:
            return int(digits[0])
        return default_num

    def download_episode(
        self,
        video_url: str,
        series_slug: str,
        episode_number: int,
        output_filename: Optional[str] = None,
    ) -> str:
        """
        Videoyu 1080p MP4 formatında indirir. Sistemde aria2c kuruluysa
        çoklu bağlantı (multi-connection) ile indirmeyi hızlandırır.
        """
        if not output_filename:
            output_filename = f"{series_slug}_ep{episode_number:03d}.mp4"
        output_path = os.path.join(self.raw_dir, output_filename)

        if os.path.exists(output_path) and os.path.getsize(output_path) > 1024 * 1024:
            logger.info("Video zaten mevcut, tekrar indirilmiyor: %s", output_path)
            return output_path

        cmd = [
            "yt-dlp",
            "-f",
            "bestvideo[height<=1080][ext=mp4]+bestaudio[ext=m4a]/best[height<=1080][ext=mp4]/best",
            "--merge-output-format",
            "mp4",
            "--no-playlist",
            "-o",
            output_path,
        ]
        if self.has_aria2c:
            cmd.extend(
                [
                    "--downloader",
                    "aria2c",
                    "--downloader-args",
                    "aria2c:-x 16 -s 16 -k 1M",
                ]
            )

        cmd.append(video_url)
        logger.info("Video indiriliyor: %s -> %s", video_url, output_path)
        subprocess.run(cmd, check=True)
        return output_path

