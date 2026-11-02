import logging
from typing import Any, Dict, List
from faster_whisper import WhisperModel
from config import CONFIG

logger = logging.getLogger("autoclip.transcriber")


class TurkishTranscriber:
    """
    Faster-Whisper (CTranslate2) kullanarak Türkçe dizi sesini kelime ve
    cümle zaman damgalarıyla metne döker. GPU (CUDA float16) yoksa otomatik
    olarak CPU (int8) moduna geçiş yapar.
    """

    def __init__(
        self,
        model_size: str = CONFIG.whisper_model,
        device: str = CONFIG.whisper_device,
        compute_type: str = CONFIG.whisper_compute_type,
    ):
        logger.info(
            "[Transcriber] Faster-Whisper yükleniyor (model=%s, device=%s, compute=%s)...",
            model_size,
            device,
            compute_type,
        )
        try:
            self.model = WhisperModel(model_size, device=device, compute_type=compute_type)
        except Exception as cuda_err:
            logger.warning(
                "CUDA başlatılamadı (%s), CPU (int8) moduna geçiliyor...", cuda_err
            )
            self.model = WhisperModel(model_size, device="cpu", compute_type="int8")

    def transcribe_video(self, video_path: str) -> List[Dict[str, Any]]:
        segments, info = self.model.transcribe(
            video_path,
            language="tr",
            beam_size=5,
            vad_filter=True,
            word_timestamps=True,
        )
        logger.info(
            "Deşifre başladı (Dil: %s, Olasılık: %.2f)",
            info.language,
            info.language_probability,
        )
        transcript: List[Dict[str, Any]] = []
        for seg in segments:
            text = seg.text.strip()
            if not text:
                continue
            words = []
            for w in seg.words or []:
                words.append(
                    {
                        "start": round(float(w.start), 2),
                        "end": round(float(w.end), 2),
                        "word": w.word.strip(),
                    }
                )
            transcript.append(
                {
                    "start": round(float(seg.start), 2),
                    "end": round(float(seg.end), 2),
                    "text": text,
                    "words": words,
                }
            )
        logger.info("Deşifre tamamlandı: %d segment üretildi.", len(transcript))
        return transcript

