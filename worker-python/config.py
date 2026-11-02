import os
from dataclasses import dataclass
from dotenv import load_dotenv

load_dotenv()


@dataclass(frozen=True)
class WorkerConfig:
    database_url: str = os.getenv(
        "DATABASE_URL",
        "postgres://fleetadmin:supersecretpassword@localhost:5432/telefleet?sslmode=disable",
    )
    ollama_url: str = os.getenv("OLLAMA_URL", "http://localhost:11434/api/generate")
    ollama_model: str = os.getenv("OLLAMA_MODEL", "qwen2.5:14b")
    whisper_model: str = os.getenv("WHISPER_MODEL", "medium")
    whisper_device: str = os.getenv("WHISPER_DEVICE", "cuda")
    whisper_compute_type: str = os.getenv("WHISPER_COMPUTE_TYPE", "float16")
    ffmpeg_use_nvenc: bool = os.getenv("FFMPEG_USE_NVENC", "true").lower() == "true"
    media_root: str = os.getenv("MEDIA_ROOT", "./media")
    poll_interval_seconds: int = int(os.getenv("POLL_INTERVAL_SECONDS", "300"))
    max_clips_per_episode: int = int(os.getenv("MAX_CLIPS_PER_EPISODE", "5"))


CONFIG = WorkerConfig()

