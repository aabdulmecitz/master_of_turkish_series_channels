import logging
import os
import subprocess
from typing import Any, Dict, List
import cv2
import mediapipe as mp
import numpy as np
from config import CONFIG

logger = logging.getLogger("autoclip.smart_renderer")


class SmartVerticalRenderer:
    """
    16:9 yatay dizi sahnelerini MediaPipe Face Detection ve Üstel Hareketli Ortalama (EMA)
    kullanarak aktif oyuncunun yüz merkezine göre 9:16 (1080x1920) dikey formata kırpar;
    üste Rusça Hook başlığı, ortaya dinamik Rusça altyazı ve alta Telegram kanal
    filigranı gömerek FFmpeg (NVENC veya libx264 fallback) ile render alır.
    """

    def __init__(self, use_nvenc: bool = CONFIG.ffmpeg_use_nvenc):
        self.use_nvenc = use_nvenc
        self.mp_face = mp.solutions.face_detection.FaceDetection(
            model_selection=1,
            min_detection_confidence=0.5,
        )

    def detect_active_face_center_x(
        self,
        video_path: str,
        start_sec: float,
        end_sec: float,
        ema_alpha: float = 0.25,
    ) -> int:
        """
        Kesit boyunca her 0.4 saniyede bir kareyi MediaPipe ile tarar,
        en öndeki/büyük yüzün X koordinatını bulur ve EMA ile yumuşatılmış
        kamera merkezini döndürür.
        """
        cap = cv2.VideoCapture(video_path)
        if not cap.isOpened():
            return 960

        width = int(cap.get(cv2.CAP_PROP_FRAME_WIDTH)) or 1920
        cap.set(cv2.CAP_PROP_POS_MSEC, start_sec * 1000.0)

        smoothed_x = float(width // 2)
        sampled_centers: List[float] = []
        current_sec = start_sec

        while current_sec < end_sec:
            ret, frame = cap.read()
            if not ret:
                break

            rgb = cv2.cvtColor(frame, cv2.COLOR_BGR2RGB)
            results = self.mp_face.process(rgb)

            if results.detections:
                # Alanı en büyük olan (kameraya en yakın/aktif) yüzü seç
                best_det = max(
                    results.detections,
                    key=lambda d: (
                        d.location_data.relative_bounding_box.width
                        * d.location_data.relative_bounding_box.height
                    ),
                )
                bbox = best_det.location_data.relative_bounding_box
                raw_center_x = (bbox.xmin + bbox.width / 2.0) * width
                smoothed_x = ema_alpha * raw_center_x + (1.0 - ema_alpha) * smoothed_x
                sampled_centers.append(smoothed_x)

            current_sec += 0.4
            cap.set(cv2.CAP_PROP_POS_MSEC, current_sec * 1000.0)

        cap.release()
        if not sampled_centers:
            return width // 2
        return int(np.median(sampled_centers))

    @staticmethod
    def generate_srt_file(
        subtitles_ru: List[Dict[str, Any]],
        clip_start: float,
        srt_path: str,
    ) -> str:
        def fmt_time(seconds: float) -> str:
            ms = int(round((seconds % 1) * 1000))
            s = int(seconds)
            m, s = divmod(s, 60)
            h, m = divmod(m, 60)
            return f"{h:02d}:{m:02d}:{s:02d},{ms:03d}"

        with open(srt_path, "w", encoding="utf-8") as f:
            for idx, sub in enumerate(subtitles_ru, start=1):
                rel_start = max(0.0, float(sub.get("start", clip_start)) - clip_start)
                rel_end = max(rel_start + 0.8, float(sub.get("end", clip_start + 2.0)) - clip_start)
                text = str(sub.get("text", "")).strip()
                if not text:
                    continue
                f.write(f"{idx}\n{fmt_time(rel_start)} --> {fmt_time(rel_end)}\n{text}\n\n")
        return srt_path

    @staticmethod
    def _escape_ffmpeg_path(path: str) -> str:
        # FFmpeg subtitles filtresi için Windows ve Linux uyumlu kaçış dizisi
        normalized = path.replace("\\", "/")
        return normalized.replace(":", "\\:").replace("'", "\\'")

    @staticmethod
    def _escape_drawtext(text: str) -> str:
        cleaned = (
            text.replace("\\", "")
            .replace("'", "")
            .replace('"', "")
            .replace(":", " -")
            .replace("%", "")
            .strip()
        )
        return cleaned[:65]

    def render_vertical_viral_clip(
        self,
        video_path: str,
        output_clip_path: str,
        start_sec: float,
        end_sec: float,
        hook_title_ru: str,
        subtitles_ru: List[Dict[str, Any]],
        watermark_handle: str,
    ) -> str:
        os.makedirs(os.path.dirname(os.path.abspath(output_clip_path)), exist_ok=True)
        center_x = self.detect_active_face_center_x(video_path, start_sec, end_sec)
        srt_path = os.path.splitext(output_clip_path)[0] + ".srt"
        self.generate_srt_file(subtitles_ru, start_sec, srt_path)

        duration = max(5.0, round(end_sec - start_sec, 2))
        safe_hook = self._escape_drawtext(hook_title_ru)
        safe_watermark = self._escape_drawtext(watermark_handle)
        escaped_srt = self._escape_ffmpeg_path(srt_path)

        vf_parts = [
            f"crop=ih*9/16:ih:clip({center_x}-(ih*9/32)\\,0\\,iw-(ih*9/16)):0",
            "scale=1080:1920",
            "drawbox=x=0:y=80:w=1080:h=150:color=black@0.75:t=fill",
            f"drawtext=text='{safe_hook}':fontcolor=white:fontsize=42:x=(w-text_w)/2:y=135",
            f"drawtext=text='{safe_watermark}':fontcolor=yellow@0.85:fontsize=34:x=(w-text_w)/2:y=1780",
        ]
        if os.path.exists(srt_path) and os.path.getsize(srt_path) > 0:
            vf_parts.append(
                f"subtitles='{escaped_srt}':force_style='Fontname=DejaVu Sans,Fontsize=18,PrimaryColour=&H0000FFFF,OutlineColour=&H00000000,BorderStyle=3,Outline=2,Alignment=2,MarginV=65'"
            )

        vf_filter = ",".join(vf_parts)
        encoder_args = (
            ["-c:v", "h264_nvenc", "-preset", "p4", "-b:v", "4.5M"]
            if self.use_nvenc
            else ["-c:v", "libx264", "-preset", "fast", "-crf", "22"]
        )

        cmd = [
            "ffmpeg",
            "-y",
            "-ss",
            str(start_sec),
            "-t",
            str(duration),
            "-i",
            video_path,
            "-vf",
            vf_filter,
            *encoder_args,
            "-c:a",
            "aac",
            "-b:a",
            "192k",
            output_clip_path,
        ]

        try:
            subprocess.run(cmd, check=True, capture_output=True, text=True)
        except subprocess.CalledProcessError as err:
            if self.use_nvenc:
                logger.warning("NVENC başarısız oldu, libx264 CPU moduna düşülüyor: %s", err.stderr)
                fallback_cmd = [
                    "ffmpeg",
                    "-y",
                    "-ss",
                    str(start_sec),
                    "-t",
                    str(duration),
                    "-i",
                    video_path,
                    "-vf",
                    vf_filter,
                    "-c:v",
                    "libx264",
                    "-preset",
                    "fast",
                    "-crf",
                    "22",
                    "-c:a",
                    "aac",
                    "-b:a",
                    "192k",
                    output_clip_path,
                ]
                subprocess.run(fallback_cmd, check=True)
            else:
                raise

        logger.info("Dikey viral kesit render edildi: %s", output_clip_path)
        return output_clip_path

