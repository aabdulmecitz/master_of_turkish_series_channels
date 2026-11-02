import logging
import os
import subprocess
import tempfile
from typing import Dict, List
import librosa
import numpy as np

logger = logging.getLogger("autoclip.audio_analyzer")


class AcousticChaosAnalyzer:
    """
    Videonun ses izini (audio track) librosa ve FFmpeg ile analiz ederek
    yüksek duygusal yoğunluk, bağırma, ağlama veya dramatik müzik zirvelerinin
    bulunduğu 45-60 saniyelik aday zaman pencerelerini tespit eder.
    """

    def __init__(
        self,
        sample_rate: int = 16000,
        window_sec: float = 45.0,
        step_sec: float = 15.0,
        peak_multiplier: float = 2.2,
    ):
        self.sample_rate = sample_rate
        self.window_sec = window_sec
        self.step_sec = step_sec
        self.peak_multiplier = peak_multiplier

    def extract_temp_wav(self, video_path: str) -> str:
        fd, wav_path = tempfile.mkstemp(suffix=".wav", prefix="autoclip_audio_")
        os.close(fd)
        cmd = [
            "ffmpeg",
            "-y",
            "-i",
            video_path,
            "-vn",
            "-ac",
            "1",
            "-ar",
            str(self.sample_rate),
            "-f",
            "wav",
            wav_path,
        ]
        subprocess.run(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
        return wav_path

    def find_dramatic_windows(
        self, video_path: str, top_k: int = 15
    ) -> List[Dict[str, float]]:
        """
        Ses enerjisinin (RMS + Spectral Flux) ortalamanın üzerine çıktığı
        en dramatik `top_k` zaman aralığını döndürür.
        """
        wav_path = ""
        try:
            wav_path = self.extract_temp_wav(video_path)
            y, sr = librosa.load(wav_path, sr=self.sample_rate, mono=True)
            total_duration = float(len(y)) / sr
            if total_duration <= self.window_sec:
                return [
                    {
                        "start_sec": 0.0,
                        "end_sec": round(total_duration, 2),
                        "energy_ratio": 3.0,
                    }
                ]

            hop_length = 512
            rms = librosa.feature.rms(y=y, frame_length=2048, hop_length=hop_length)[0]
            onset_env = librosa.onset.onset_strength(y=y, sr=sr, hop_length=hop_length)

            mean_rms = float(np.mean(rms)) + 1e-8
            mean_onset = float(np.mean(onset_env)) + 1e-8

            frames_per_sec = sr / float(hop_length)
            win_frames = int(self.window_sec * frames_per_sec)
            step_frames = max(1, int(self.step_sec * frames_per_sec))

            candidates: List[Dict[str, float]] = []
            for start_frame in range(0, max(1, len(rms) - win_frames), step_frames):
                end_frame = min(len(rms), start_frame + win_frames)
                seg_rms = rms[start_frame:end_frame]
                seg_onset = onset_env[start_frame:end_frame]

                peak_rms_ratio = float(np.percentile(seg_rms, 90)) / mean_rms
                onset_ratio = float(np.percentile(seg_onset, 90)) / mean_onset
                combined_score = round(0.65 * peak_rms_ratio + 0.35 * onset_ratio, 3)

                start_sec = round(start_frame / frames_per_sec, 2)
                end_sec = round(min(total_duration, start_sec + self.window_sec), 2)

                candidates.append(
                    {
                        "start_sec": start_sec,
                        "end_sec": end_sec,
                        "energy_ratio": combined_score,
                    }
                )

            # En yüksek enerjili pencereleri seç ve çakışanları filtrele
            candidates.sort(key=lambda item: item["energy_ratio"], reverse=True)
            selected: List[Dict[str, float]] = []
            for cand in candidates:
                overlaps = any(
                    abs(cand["start_sec"] - existing["start_sec"]) < (self.window_sec * 0.6)
                    for existing in selected
                )
                if not overlaps:
                    selected.append(cand)
                if len(selected) >= top_k:
                    break

            selected.sort(key=lambda item: item["start_sec"])
            logger.info(
                "Akustik analiz tamamlandı: %d dramatik aday pencere bulundu.",
                len(selected),
            )
            return selected
        except Exception as exc:
            logger.warning("Akustik analizde hata oluştu, varsayılan pencereler kullanılacak: %s", exc)
            return []
        finally:
            if wav_path and os.path.exists(wav_path):
                try:
                    os.remove(wav_path)
                except OSError:
                    pass

