import logging
from contextlib import contextmanager
from datetime import datetime, timedelta, timezone
from typing import Any, Dict, List, Optional
import psycopg2
from psycopg2.extras import RealDictCursor
from config import CONFIG

logger = logging.getLogger("autoclip.db")


@contextmanager
def get_db_connection():
    conn = psycopg2.connect(CONFIG.database_url, cursor_factory=RealDictCursor)
    try:
        yield conn
        conn.commit()
    except Exception:
        conn.rollback()
        raise
    finally:
        conn.close()


def fetch_active_series() -> List[Dict[str, Any]]:
    with get_db_connection() as conn:
        with conn.cursor() as cur:
            cur.execute(
                """
                SELECT *
                FROM series_fleet
                WHERE is_active = TRUE
                ORDER BY created_at ASC
                """
            )
            return list(cur.fetchall())


def mark_series_checked(series_id: str) -> None:
    with get_db_connection() as conn:
        with conn.cursor() as cur:
            cur.execute(
                "UPDATE series_fleet SET last_checked_at = NOW() WHERE id = %s",
                (series_id,),
            )


def episode_exists(source_video_id: str) -> bool:
    with get_db_connection() as conn:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT 1 FROM episodes WHERE source_video_id = %s LIMIT 1",
                (source_video_id,),
            )
            return cur.fetchone() is not None


def create_detected_episode(
    series_id: str,
    episode_number: int,
    episode_code: str,
    source_video_id: str,
) -> Dict[str, Any]:
    with get_db_connection() as conn:
        with conn.cursor() as cur:
            cur.execute(
                """
                INSERT INTO episodes (series_id, episode_number, episode_code, source_video_id, status)
                VALUES (%s, %s, %s, %s, 'detected')
                ON CONFLICT (source_video_id) DO UPDATE
                    SET updated_at = NOW()
                RETURNING *
                """,
                (series_id, episode_number, episode_code, source_video_id),
            )
            return dict(cur.fetchone())


def fetch_pending_episodes() -> List[Dict[str, Any]]:
    with get_db_connection() as conn:
        with conn.cursor() as cur:
            cur.execute(
                """
                SELECT e.*,
                       s.slug AS series_slug,
                       s.title_tr,
                       s.title_ru,
                       s.source_url,
                       s.hub_channel_id,
                       s.hub_channel_username,
                       s.active_spoke_channel_id
                FROM episodes e
                JOIN series_fleet s ON s.id = e.series_id
                WHERE e.status IN ('detected', 'downloading', 'transcribing', 'clipping')
                ORDER BY e.created_at ASC
                LIMIT 5
                """
            )
            return list(cur.fetchall())


def update_episode_status(
    episode_id: str,
    status: str,
    raw_file_path: Optional[str] = None,
    subtitled_file_path: Optional[str] = None,
    stream_url: Optional[str] = None,
    error_message: Optional[str] = None,
) -> None:
    with get_db_connection() as conn:
        with conn.cursor() as cur:
            cur.execute(
                """
                UPDATE episodes
                SET status = %s,
                    raw_file_path = COALESCE(%s, raw_file_path),
                    subtitled_file_path = COALESCE(%s, subtitled_file_path),
                    stream_url = COALESCE(%s, stream_url),
                    error_message = %s,
                    updated_at = NOW()
                WHERE id = %s
                """,
                (
                    status,
                    raw_file_path,
                    subtitled_file_path,
                    stream_url,
                    error_message,
                    episode_id,
                ),
            )


def insert_viral_clip(
    episode_id: str,
    clip_index: int,
    start_sec: float,
    end_sec: float,
    viral_score: float,
    hook_title_ru: str,
    caption_ru: str,
    clip_file_path: str,
    schedule_offset_minutes: int = 0,
) -> Dict[str, Any]:
    scheduled_at = datetime.now(timezone.utc) + timedelta(minutes=schedule_offset_minutes)
    with get_db_connection() as conn:
        with conn.cursor() as cur:
            cur.execute(
                """
                INSERT INTO ai_viral_clips (
                    episode_id, clip_index, start_sec, end_sec, viral_score,
                    hook_title_ru, caption_ru, clip_file_path, scheduled_at, is_posted
                )
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, FALSE)
                RETURNING *
                """,
                (
                    episode_id,
                    clip_index,
                    start_sec,
                    end_sec,
                    viral_score,
                    hook_title_ru,
                    caption_ru,
                    clip_file_path,
                    scheduled_at,
                ),
            )
            return dict(cur.fetchone())

