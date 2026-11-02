-- ============================================================================
-- TeleFleet & AutoClip AI - PostgreSQL Veritabanı Şeması (001_init.sql)
-- ============================================================================

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- 1. DİZİLER VE KANAL FİLOSU (MULTI-SERIES FLEET)
CREATE TABLE IF NOT EXISTS series_fleet (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    slug VARCHAR(64) UNIQUE NOT NULL,               -- Örn: "yalicapkini"
    title_tr VARCHAR(255) NOT NULL,                 -- Örn: "Yalı Çapkını"
    title_ru VARCHAR(255) NOT NULL,                 -- Örn: "Зимородок"
    source_url TEXT NOT NULL,                       -- Resmi YouTube kanal/playlist veya medya RSS kaynağı
    hub_channel_id BIGINT NOT NULL,                 -- Herkese açık vitrin kanalı ID'si
    hub_channel_username VARCHAR(128) NOT NULL,     -- Örn: "@zimorodok_ru_hub"
    active_spoke_channel_id BIGINT NOT NULL,        -- Uzun format içerik/arşiv kanalı ID'si
    active_spoke_invite_link TEXT NOT NULL,         -- Dinamik davet linki
    backup_spoke_channel_id BIGINT,                 -- Yedek arşiv kanalı ID'si
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    last_checked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 2. BÖLÜMLER VE AI İŞLEME DURUMU (EPISODES)
CREATE TABLE IF NOT EXISTS episodes (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    series_id UUID NOT NULL REFERENCES series_fleet(id) ON DELETE CASCADE,
    episode_number INT NOT NULL,
    episode_code VARCHAR(128) UNIQUE NOT NULL,      -- Örn: "ep_yalicapkini_65"
    source_video_id VARCHAR(128) UNIQUE NOT NULL,   -- YouTube / Kaynak video ID
    raw_file_path TEXT,
    subtitled_file_path TEXT,
    spoke_message_id BIGINT,                        -- Arşiv kanalındaki mesaj ID'si
    stream_url TEXT,                                -- Web Player HLS/MP4 yayın URL'si
    status VARCHAR(32) NOT NULL DEFAULT 'detected'
        CHECK (status IN ('detected', 'downloading', 'transcribing', 'clipping', 'uploading', 'published', 'failed')),
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_episodes_status ON episodes(status);
CREATE INDEX IF NOT EXISTS idx_episodes_series_id ON episodes(series_id);

-- 3. LOKAL AI TARAFINDAN ÜRETİLEN VİRAL KESİTLER (9:16 SHORTS/REELS/HUB)
CREATE TABLE IF NOT EXISTS ai_viral_clips (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    episode_id UUID NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
    clip_index INT NOT NULL,
    start_sec NUMERIC(8,2) NOT NULL,
    end_sec NUMERIC(8,2) NOT NULL,
    viral_score NUMERIC(5,2) NOT NULL,
    hook_title_ru TEXT NOT NULL,                    -- Ollama tarafından üretilen Rusça başlık
    caption_ru TEXT NOT NULL,                       -- Telegram post açıklaması
    clip_file_path TEXT NOT NULL,
    hub_message_id BIGINT,
    scheduled_at TIMESTAMPTZ NOT NULL,
    is_posted BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_clips_scheduled ON ai_viral_clips(is_posted, scheduled_at);

-- 4. MONETİZASYON VE SPONSOR KAMPANYALARI (PRE-ROLL & SPONSORED LINKS)
CREATE TABLE IF NOT EXISTS monetization_targets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    campaign_type VARCHAR(32) NOT NULL
        CHECK (campaign_type IN ('youtube_preroll', 'sponsor_banner', 'cpa_offer')),
    title VARCHAR(255) NOT NULL DEFAULT 'Sponsorlu İçerik',
    target_url TEXT NOT NULL,                       -- YouTube Video ID veya Sponsor URL
    required_watch_sec INT NOT NULL DEFAULT 30,
    weight INT NOT NULL DEFAULT 100,                -- Trafik dağıtım ağırlığı
    total_impressions BIGINT NOT NULL DEFAULT 0,
    total_completions BIGINT NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 5. KULLANICI HUNİSİ VE TELEGRAM STARS VIP ABONELİKLERİ
CREATE TABLE IF NOT EXISTS telegram_users (
    telegram_id BIGINT PRIMARY KEY,
    username VARCHAR(128),
    first_name VARCHAR(128),
    first_seen_series_id UUID REFERENCES series_fleet(id),
    is_vip BOOLEAN NOT NULL DEFAULT FALSE,
    vip_expires_at TIMESTAMPTZ,
    total_ad_views INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 6. ÇAPRAZ TRAFİK (CROSS-PROMO) MESAJ KAYITLARI (6 Saat Sonra Otomatik Silme İçin)
CREATE TABLE IF NOT EXISTS cross_promo_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    source_channel_id BIGINT NOT NULL,
    source_message_id BIGINT NOT NULL,
    target_channel_id BIGINT NOT NULL,
    forwarded_message_id BIGINT NOT NULL,
    delete_after TIMESTAMPTZ NOT NULL,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cross_promo_cleanup ON cross_promo_logs(is_deleted, delete_after);

