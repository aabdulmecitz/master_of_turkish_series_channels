import json
import logging
from typing import Any, Dict, List
import requests
from config import CONFIG

logger = logging.getLogger("autoclip.ollama_director")


class OllamaDirector:
    """
    Sıfır API maliyetiyle yerel Ollama sunucusu (qwen2.5:14b / llama3.1:8b) üzerinden:
    1) Akustik analiz ve Türkçe transkripti birleştirerek en yüksek merak uyandırıcı
       (cliffhanger) 5 sahneyi seçer.
    2) Seçilen sahnelerin diyaloglarını doğal, duygusal Rusçaya çevirir.
    3) Her kesit için 6 kelimelik kışkırtıcı Rusça başlık (hook_title_ru) ve
       Telegram açıklaması (caption_ru) üretir.
    """

    def __init__(
        self,
        ollama_url: str = CONFIG.ollama_url,
        model_name: str = CONFIG.ollama_model,
    ):
        self.ollama_url = ollama_url
        self.model_name = model_name

    def _filter_segments_for_windows(
        self,
        transcript: List[Dict[str, Any]],
        dramatic_windows: List[Dict[str, float]],
    ) -> List[Dict[str, Any]]:
        if not dramatic_windows:
            return transcript[:350]

        filtered: List[Dict[str, Any]] = []
        seen_starts = set()
        for win in dramatic_windows:
            w_start = win["start_sec"] - 5.0
            w_end = win["end_sec"] + 5.0
            for seg in transcript:
                if seg["end"] >= w_start and seg["start"] <= w_end:
                    if seg["start"] not in seen_starts:
                        seen_starts.add(seg["start"])
                        filtered.append(seg)

        if len(filtered) < 15:
            return transcript[:350]

        filtered.sort(key=lambda item: item["start"])
        return filtered[:400]

    def select_viral_clips_and_translate_ru(
        self,
        transcript: List[Dict[str, Any]],
        dramatic_windows: List[Dict[str, float]],
        series_name_ru: str,
        max_clips: int = CONFIG.max_clips_per_episode,
    ) -> List[Dict[str, Any]]:
        candidate_segments = self._filter_segments_for_windows(transcript, dramatic_windows)
        condensed_text = "\n".join(
            [f"[{s['start']}s - {s['end']}s]: {s['text']}" for s in candidate_segments]
        )
        acoustic_hints = "\n".join(
            [
                f"- [{w['start_sec']}s - {w['end_sec']}s] (Akustik Enerji Skoru: {w['energy_ratio']})"
                for w in dramatic_windows[:15]
            ]
        )

        prompt = f"""Sen viral bir Rusça Türk dizisi kurgu yönetmenisin. Dizi adı: {series_name_ru}.
Aşağıda bölümün akustik zirve noktaları (bağırma, ağlama, müzik patlaması) ve Türkçe diyalog transkripti yer alıyor.
Kavga, ihanet, sır ifşası, büyük aşk itirafı veya en yüksek gerilim (cliffhanger) içeren EN VİRAL {max_clips} kesiti (her biri 35 ile 58 saniye arasında olmalı) seç.

Her kesit için:
1. Başlangıç (`start_sec`) ve bitiş (`end_sec`) saniyesini belirle.
2. Videonun üst kısmına yazılacak maksimum 6 kelimelik çok çarpıcı Rusça başlık (`hook_title_ru`) yaz.
3. Telegram paylaşımının altına yazılacak heyecanlı Rusça açıklama (`caption_ru`) yaz.
4. Kesit içindeki diyalogları doğal ve duygusal Rusçaya (`subtitles_ru`) çevir.

SADECE aşağıdaki JSON şemasında yanıt ver:
{{
  "clips": [
    {{
      "start_sec": 120.5,
      "end_sec": 168.0,
      "viral_score": 95.0,
      "hook_title_ru": "Узнала об измене прямо на свадьбе! 😱",
      "caption_ru": "Такого поворота в сериале «{series_name_ru}» никто не ожидал! 🔥 Полная серия уже доступна по кнопке ниже 👇",
      "subtitles_ru": [
        {{"start": 120.5, "end": 124.2, "text": "Ты думал, я никогда не узнаю правду?!"}}
      ]
    }}
  ]
}}

AKUSTİK ZİRVE PENCERELERİ:
{acoustic_hints}

TÜRKÇE TRANSKRİPT:
{condensed_text}
"""
        try:
            resp = requests.post(
                self.ollama_url,
                json={
                    "model": self.model_name,
                    "prompt": prompt,
                    "format": "json",
                    "stream": False,
                    "options": {"temperature": 0.3, "num_ctx": 8192},
                },
                timeout=300,
            )
            resp.raise_for_status()
            payload = resp.json()
            parsed = json.loads(payload.get("response", "{}"))
            clips = parsed.get("clips", [])
            if clips:
                return clips[:max_clips]
        except Exception as exc:
            logger.error("Ollama viral kesit seçiminde hata: %s", exc)

        # Ollama erişilemezse veya boş dönerse akustik zirvelerden yedek kesit üret
        return self._fallback_clips_from_acoustics(
            transcript, dramatic_windows, series_name_ru, max_clips
        )

    def translate_full_transcript_to_ru(
        self, transcript: List[Dict[str, Any]], batch_size: int = 40
    ) -> List[Dict[str, Any]]:
        """
        Tam bölümün Türkçe altyazılarını gruplar halinde yerel Ollama ile Rusçaya çevirir.
        """
        translated_all: List[Dict[str, Any]] = []
        for i in range(0, len(transcript), batch_size):
            batch = transcript[i : i + batch_size]
            lines_input = [
                {"idx": idx, "start": s["start"], "end": s["end"], "text": s["text"]}
                for idx, s in enumerate(batch)
            ]
            prompt = (
                "Aşağıdaki Türkçe dizi altyazılarını duygusal tonu koruyarak doğal Rusçaya çevir.\n"
                "SADECE JSON formatında `{\"items\": [{\"idx\": 0, \"text_ru\": \"...\"}]}` olarak yanıt ver:\n"
                f"{json.dumps(lines_input, ensure_ascii=False)}"
            )
            try:
                resp = requests.post(
                    self.ollama_url,
                    json={
                        "model": self.model_name,
                        "prompt": prompt,
                        "format": "json",
                        "stream": False,
                        "options": {"temperature": 0.2},
                    },
                    timeout=180,
                )
                resp.raise_for_status()
                items = json.loads(resp.json().get("response", "{}")).get("items", [])
                ru_map = {int(item["idx"]): item.get("text_ru", "") for item in items if "idx" in item}
                for idx, orig in enumerate(batch):
                    translated_all.append(
                        {
                            "start": orig["start"],
                            "end": orig["end"],
                            "text": ru_map.get(idx) or orig["text"],
                        }
                    )
            except Exception as exc:
                logger.warning("Altyazı batch çevirisinde hata (%d): %s", i, exc)
                for orig in batch:
                    translated_all.append(
                        {"start": orig["start"], "end": orig["end"], "text": orig["text"]}
                    )
        return translated_all

    @staticmethod
    def _fallback_clips_from_acoustics(
        transcript: List[Dict[str, Any]],
        dramatic_windows: List[Dict[str, float]],
        series_name_ru: str,
        max_clips: int,
    ) -> List[Dict[str, Any]]:
        fallback: List[Dict[str, Any]] = []
        windows = dramatic_windows[:max_clips] if dramatic_windows else [
            {"start_sec": 60.0, "end_sec": 110.0, "energy_ratio": 2.5}
        ]
        for idx, win in enumerate(windows, start=1):
            start_s = float(win["start_sec"])
            end_s = float(win["end_sec"])
            subs = [
                {"start": s["start"], "end": s["end"], "text": s["text"]}
                for s in transcript
                if s["end"] >= start_s and s["start"] <= end_s
            ]
            fallback.append(
                {
                    "start_sec": start_s,
                    "end_sec": end_s,
                    "viral_score": round(min(99.0, 75.0 + float(win.get("energy_ratio", 2.0)) * 5), 2),
                    "hook_title_ru": f"Самый напряженный момент! ({series_name_ru}) 🔥",
                    "caption_ru": f"Невероятная сцена из сериала «{series_name_ru}»! 😱 Смотрите полную серию по кнопке ниже 👇",
                    "subtitles_ru": subs,
                }
            )
        return fallback

