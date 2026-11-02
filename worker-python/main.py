import logging
import os
import time
from typing import Any, Dict
from config import CONFIG
import db
from fetcher import MediaFetcher
from audio_analyzer import AcousticChaosAnalyzer
from transcriber import TurkishTranscriber
from ollama_director import OllamaDirector
from smart_renderer import SmartVerticalRenderer

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
)
logger = logging.getLogger("autoclip.main")


class AutoClipOrchestrator:
    """
    Uçtan uca Lokal AI Medya Fabrikası Orkestratörü:
    1. Aktif dizilerin kaynaklarını (series_fleet) tarar ve yeni bölümleri tespit eder.
    2. Yeni bölümleri yt-dlp ile indirir (downloading).
    3. Faster-Whisper ile Türkçe ses izini deşifre eder + Librosa ile akustik zirveleri bulur (transcribing).
    4. Yerel Ollama ile en dramatik 5 kesiti seçer ve Rusça çevirilerini üretir (clipping).
    5. MediaPipe + FFmpeg NVENC ile 9:16 dikey kesitleri render edip veritabanına (ai_viral_clips) yazar.
    """

    def __init__(self):
        self.fetcher = MediaFetcher(CONFIG.media_root)
        self.audio_analyzer = AcousticChaosAnalyzer()
        self.transcriber = TurkishTranscriber()
        self.director = OllamaDirector()
        self.renderer = SmartVerticalRenderer()
        self.clips_dir = os.path.join(CONFIG.media_root, "clips")
        self.subtitled_dir = os.path.join(CONFIG.media_root, "episodes_ru")
        os.makedirs(self.clips_dir, exist_ok=True)
        os.makedirs(self.subtitled_dir, exist_ok=True)

    def discover_new_episodes(self) -> None:
        series_list = db.fetch_active_series()
        for series in series_list:
            series_id = str(series["id"])
            slug = series["slug"]
            source_url = series["source_url"]
            logger.info("[%s] Kaynak kontrol ediliyor: %s", slug, source_url)

            entries = self.fetcher.extract_latest_entries(source_url, max_entries=3)
            for entry in entries:
                vid_id = entry["video_id"]
                if not db.episode_exists(vid_id):
                    ep_num = entry["episode_number"]
                    ep_code = f"ep_{slug}_{ep_num}_{vid_id[:6]}"
                    db.create_detected_episode(
                        series_id=series_id,
                        episode_number=ep_num,
                        episode_code=ep_code,
                        source_video_id=vid_id,
                    )
                    logger.info(
                        "[%s] Yeni bölüm tespit edildi: Bölüm %d (kod=%s)",
                        slug,
                        ep_num,
                        ep_code,
                    )
            db.mark_series_checked(series_id)

    def process_episode(self, ep: Dict[str, Any]) -> None:
        episode_id = str(ep["id"])
        slug = ep["series_slug"]
        ep_num = int(ep["episode_number"])
        ep_code = ep["episode_code"]
        title_ru = ep["title_ru"]
        watermark = ep["hub_channel_username"]
        vid_id = ep["source_video_id"]

        video_url = (
            vid_id
            if vid_id.startswith("http")
            else f"https://www.youtube.com/watch?v={vid_id}"
        )

        try:
            # ADIM 1: İndirme
            db.update_episode_status(episode_id, "downloading")
            raw_path = self.fetcher.download_episode(
                video_url=video_url,
                series_slug=slug,
                episode_number=ep_num,
                output_filename=f"{ep_code}.mp4",
            )
            db.update_episode_status(episode_id, "transcribing", raw_file_path=raw_path)

            # ADIM 2: Akustik Kaos Analizi + Faster-Whisper Türkçe Deşifre
            dramatic_windows = self.audio_analyzer.find_dramatic_windows(raw_path, top_k=15)
            transcript = self.transcriber.transcribe_video(raw_path)

            # ADIM 3: Lokal Ollama ile Viral Sahne Seçimi ve Rusça Çeviri
            db.update_episode_status(episode_id, "clipping")
            viral_clips = self.director.select_viral_clips_and_translate_ru(
                transcript=transcript,
                dramatic_windows=dramatic_windows,
                series_name_ru=title_ru,
                max_clips=CONFIG.max_clips_per_episode,
            )

            # Tam bölüm Rusça SRT dosyasını da oluştur
            full_ru_subs = self.director.translate_full_transcript_to_ru(transcript)
            full_srt_path = os.path.join(self.subtitled_dir, f"{ep_code}_ru.srt")
            self.renderer.generate_srt_file(full_ru_subs, 0.0, full_srt_path)

            # ADIM 4: MediaPipe 9:16 Akıllı Kadrajlama + FFmpeg Render
            for idx, clip_meta in enumerate(viral_clips, start=1):
                start_sec = float(clip_meta.get("start_sec", 60.0))
                end_sec = float(clip_meta.get("end_sec", start_sec + 45.0))
                if end_sec <= start_sec:
                    end_sec = start_sec + 45.0

                clip_filename = f"{ep_code}_clip_{idx:02d}.mp4"
                clip_output_path = os.path.join(self.clips_dir, clip_filename)

                self.renderer.render_vertical_viral_clip(
                    video_path=raw_path,
                    output_clip_path=clip_output_path,
                    start_sec=start_sec,
                    end_sec=end_sec,
                    hook_title_ru=clip_meta.get("hook_title_ru", f"{title_ru} 🔥"),
                    subtitles_ru=clip_meta.get("subtitles_ru", []),
                    watermark_handle=watermark,
                )

                # Kesitleri 45'er dakika arayla Hub kanalına düşecek şekilde zamanla
                db.insert_viral_clip(
                    episode_id=episode_id,
                    clip_index=idx,
                    start_sec=start_sec,
                    end_sec=end_sec,
                    viral_score=float(clip_meta.get("viral_score", 90.0)),
                    hook_title_ru=clip_meta.get("hook_title_ru", f"{title_ru} 🔥"),
                    caption_ru=clip_meta.get(
                        "caption_ru",
                        f"🔥 Новая серия «{title_ru}» уже доступна по кнопке ниже!",
                    ),
                    clip_file_path=clip_output_path,
                    schedule_offset_minutes=(idx - 1) * 45,
                )

            stream_rel_url = f"/media/raw/{os.path.basename(raw_path)}"
            db.update_episode_status(
                episode_id=episode_id,
                status="uploading",
                raw_file_path=raw_path,
                subtitled_file_path=full_srt_path,
                stream_url=stream_rel_url,
            )
            logger.info(
                "[%s] Bölüm işleme tamamlandı (%d viral kesit üretildi).",
                ep_code,
                len(viral_clips),
            )
        except Exception as exc:
            logger.exception("[%s] Bölüm işlenirken hata oluştu: %s", ep_code, exc)
            db.update_episode_status(
                episode_id=episode_id,
                status="failed",
                error_message=str(exc)[:1000],
            )

    def run_forever(self) -> None:
        logger.info("AutoClip Lokal AI Medya Fabrikası başlatıldı.")
        while True:
            try:
                self.discover_new_episodes()
                pending = db.fetch_pending_episodes()
                for ep in pending:
                    self.process_episode(ep)
            except Exception as loop_err:
                logger.error("Ana döngü hatası: %s", loop_err)

            time.sleep(CONFIG.poll_interval_seconds)


if __name__ == "__main__":
    orchestrator = AutoClipOrchestrator()
    orchestrator.run_forever()

