package main

import (
	"database/sql"
	"time"

	_ "github.com/lib/pq"
)

type Repository struct {
	db *sql.DB
}

type SeriesRecord struct {
	ID                    string
	Slug                  string
	TitleTR               string
	TitleRU               string
	HubChannelID          int64
	HubChannelUsername    string
	ActiveSpokeChannelID  int64
	ActiveSpokeInviteLink string
	BackupSpokeChannelID  sql.NullInt64
}

type DueClipRecord struct {
	ClipID         string
	EpisodeID      string
	EpisodeCode    string
	HookTitleRU    string
	CaptionRU      string
	ClipFilePath   string
	HubChannelID   int64
	SeriesTitleRU  string
}

type EpisodeDetail struct {
	ID                    string
	SeriesID              string
	EpisodeNumber         int
	EpisodeCode           string
	TitleRU               string
	RawFilePath           string
	SubtitledFilePath     string
	StreamURL             string
	ActiveSpokeChannelID  int64
	ActiveSpokeInviteLink string
	SpokeMessageID        sql.NullInt64
}

type MonetizationTarget struct {
	ID               string `json:"id"`
	CampaignType     string `json:"campaign_type"`
	Title            string `json:"title"`
	TargetURL        string `json:"target_url"`
	RequiredWatchSec int    `json:"required_watch_sec"`
}

type CrossPromoDeleteRecord struct {
	ID                 string
	TargetChannelID    int64
	ForwardedMessageID int64
}

func NewRepository(databaseURL string) (*Repository, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(15)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Close() error {
	return r.db.Close()
}

func (r *Repository) FetchDueViralClips(limit int) ([]DueClipRecord, error) {
	rows, err := r.db.Query(`
		SELECT c.id, c.episode_id, e.episode_code, c.hook_title_ru, c.caption_ru,
		       c.clip_file_path, s.hub_channel_id, s.title_ru
		FROM ai_viral_clips c
		JOIN episodes e ON e.id = c.episode_id
		JOIN series_fleet s ON s.id = e.series_id
		WHERE c.is_posted = FALSE AND c.scheduled_at <= NOW()
		ORDER BY c.scheduled_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []DueClipRecord
	for rows.Next() {
		var rec DueClipRecord
		if err := rows.Scan(
			&rec.ClipID,
			&rec.EpisodeID,
			&rec.EpisodeCode,
			&rec.HookTitleRU,
			&rec.CaptionRU,
			&rec.ClipFilePath,
			&rec.HubChannelID,
			&rec.SeriesTitleRU,
		); err != nil {
			return nil, err
		}
		result = append(result, rec)
	}
	return result, nil
}

func (r *Repository) MarkClipPosted(clipID string, hubMessageID int64) error {
	_, err := r.db.Exec(`
		UPDATE ai_viral_clips
		SET is_posted = TRUE, hub_message_id = $2
		WHERE id = $1
	`, clipID, hubMessageID)
	return err
}

func (r *Repository) FetchUploadingEpisodes(limit int) ([]EpisodeDetail, error) {
	rows, err := r.db.Query(`
		SELECT e.id, e.series_id, e.episode_number, e.episode_code, s.title_ru,
		       COALESCE(e.raw_file_path, ''), COALESCE(e.subtitled_file_path, ''),
		       COALESCE(e.stream_url, ''), s.active_spoke_channel_id,
		       s.active_spoke_invite_link, e.spoke_message_id
		FROM episodes e
		JOIN series_fleet s ON s.id = e.series_id
		WHERE e.status = 'uploading'
		ORDER BY e.created_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []EpisodeDetail
	for rows.Next() {
		var ep EpisodeDetail
		if err := rows.Scan(
			&ep.ID,
			&ep.SeriesID,
			&ep.EpisodeNumber,
			&ep.EpisodeCode,
			&ep.TitleRU,
			&ep.RawFilePath,
			&ep.SubtitledFilePath,
			&ep.StreamURL,
			&ep.ActiveSpokeChannelID,
			&ep.ActiveSpokeInviteLink,
			&ep.SpokeMessageID,
		); err != nil {
			return nil, err
		}
		list = append(list, ep)
	}
	return list, nil
}

func (r *Repository) MarkEpisodePublished(episodeID string, spokeMessageID int64) error {
	_, err := r.db.Exec(`
		UPDATE episodes
		SET status = 'published', spoke_message_id = $2, updated_at = NOW()
		WHERE id = $1
	`, episodeID, spokeMessageID)
	return err
}

func (r *Repository) GetEpisodeByCode(episodeCode string) (*EpisodeDetail, error) {
	var ep EpisodeDetail
	err := r.db.QueryRow(`
		SELECT e.id, e.series_id, e.episode_number, e.episode_code, s.title_ru,
		       COALESCE(e.raw_file_path, ''), COALESCE(e.subtitled_file_path, ''),
		       COALESCE(e.stream_url, ''), s.active_spoke_channel_id,
		       s.active_spoke_invite_link, e.spoke_message_id
		FROM episodes e
		JOIN series_fleet s ON s.id = e.series_id
		WHERE e.episode_code = $1
		LIMIT 1
	`, episodeCode).Scan(
		&ep.ID,
		&ep.SeriesID,
		&ep.EpisodeNumber,
		&ep.EpisodeCode,
		&ep.TitleRU,
		&ep.RawFilePath,
		&ep.SubtitledFilePath,
		&ep.StreamURL,
		&ep.ActiveSpokeChannelID,
		&ep.ActiveSpokeInviteLink,
		&ep.SpokeMessageID,
	)
	if err != nil {
		return nil, err
	}
	return &ep, nil
}

func (r *Repository) UpsertTelegramUser(telegramID int64, username, firstName string, seriesID *string) error {
	_, err := r.db.Exec(`
		INSERT INTO telegram_users (telegram_id, username, first_name, first_seen_series_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (telegram_id) DO UPDATE
		SET username = COALESCE(EXCLUDED.username, telegram_users.username),
		    first_name = COALESCE(EXCLUDED.first_name, telegram_users.first_name),
		    updated_at = NOW()
	`, telegramID, username, firstName, seriesID)
	return err
}

func (r *Repository) IsUserVIP(telegramID int64) (bool, error) {
	var isVIP bool
	var expiresAt sql.NullTime
	err := r.db.QueryRow(`
		SELECT is_vip, vip_expires_at
		FROM telegram_users
		WHERE telegram_id = $1
	`, telegramID).Scan(&isVIP, &expiresAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !isVIP {
		return false, nil
	}
	if expiresAt.Valid && expiresAt.Time.Before(time.Now()) {
		return false, nil
	}
	return true, nil
}

func (r *Repository) ActivateUserVIP(telegramID int64, durationDays int) error {
	expires := time.Now().UTC().Add(time.Duration(durationDays) * 24 * time.Hour)
	_, err := r.db.Exec(`
		INSERT INTO telegram_users (telegram_id, is_vip, vip_expires_at, updated_at)
		VALUES ($1, TRUE, $2, NOW())
		ON CONFLICT (telegram_id) DO UPDATE
		SET is_vip = TRUE, vip_expires_at = $2, updated_at = NOW()
	`, telegramID, expires)
	return err
}

func (r *Repository) SelectActiveMonetizationTarget(campaignType string) (*MonetizationTarget, error) {
	var t MonetizationTarget
	err := r.db.QueryRow(`
		SELECT id, campaign_type, title, target_url, required_watch_sec
		FROM monetization_targets
		WHERE is_active = TRUE AND campaign_type = $1
		ORDER BY (RANDOM() * weight) DESC
		LIMIT 1
	`, campaignType).Scan(&t.ID, &t.CampaignType, &t.Title, &t.TargetURL, &t.RequiredWatchSec)
	if err != nil {
		return nil, err
	}
	_, _ = r.db.Exec(`UPDATE monetization_targets SET total_impressions = total_impressions + 1 WHERE id = $1`, t.ID)
	return &t, nil
}

func (r *Repository) RecordAdCompletion(campaignID string, telegramID int64) error {
	if campaignID != "" {
		_, _ = r.db.Exec(`
			UPDATE monetization_targets
			SET total_completions = total_completions + 1
			WHERE id = $1
		`, campaignID)
	}
	if telegramID > 0 {
		_, _ = r.db.Exec(`
			UPDATE telegram_users
			SET total_ad_views = total_ad_views + 1, updated_at = NOW()
			WHERE telegram_id = $1
		`, telegramID)
	}
	return nil
}

func (r *Repository) FetchActiveSeriesFleet() ([]SeriesRecord, error) {
	rows, err := r.db.Query(`
		SELECT id, slug, title_tr, title_ru, hub_channel_id, hub_channel_username,
		       active_spoke_channel_id, active_spoke_invite_link, backup_spoke_channel_id
		FROM series_fleet
		WHERE is_active = TRUE
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []SeriesRecord
	for rows.Next() {
		var s SeriesRecord
		if err := rows.Scan(
			&s.ID,
			&s.Slug,
			&s.TitleTR,
			&s.TitleRU,
			&s.HubChannelID,
			&s.HubChannelUsername,
			&s.ActiveSpokeChannelID,
			&s.ActiveSpokeInviteLink,
			&s.BackupSpokeChannelID,
		); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, nil
}

func (r *Repository) SwitchToBackupSpoke(seriesID string, newSpokeID int64, newInviteLink string) error {
	_, err := r.db.Exec(`
		UPDATE series_fleet
		SET active_spoke_channel_id = $2,
		    active_spoke_invite_link = $3,
		    backup_spoke_channel_id = NULL
		WHERE id = $1
	`, seriesID, newSpokeID, newInviteLink)
	return err
}

func (r *Repository) LogCrossPromoForward(sourceCh, sourceMsg, targetCh, forwardedMsg int64, deleteAfter time.Time) error {
	_, err := r.db.Exec(`
		INSERT INTO cross_promo_logs (source_channel_id, source_message_id, target_channel_id, forwarded_message_id, delete_after)
		VALUES ($1, $2, $3, $4, $5)
	`, sourceCh, sourceMsg, targetCh, forwardedMsg, deleteAfter)
	return err
}

func (r *Repository) FetchExpiredCrossPromos() ([]CrossPromoDeleteRecord, error) {
	rows, err := r.db.Query(`
		SELECT id, target_channel_id, forwarded_message_id
		FROM cross_promo_logs
		WHERE is_deleted = FALSE AND delete_after <= NOW()
		LIMIT 50
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []CrossPromoDeleteRecord
	for rows.Next() {
		var rec CrossPromoDeleteRecord
		if err := rows.Scan(&rec.ID, &rec.TargetChannelID, &rec.ForwardedMessageID); err != nil {
			return nil, err
		}
		list = append(list, rec)
	}
	return list, nil
}

func (r *Repository) MarkCrossPromoDeleted(id string) error {
	_, err := r.db.Exec(`UPDATE cross_promo_logs SET is_deleted = TRUE WHERE id = $1`, id)
	return err
}

func (r *Repository) FetchTopClipPerHubChannel() (map[int64]int64, error) {
	rows, err := r.db.Query(`
		SELECT DISTINCT ON (s.hub_channel_id) s.hub_channel_id, c.hub_message_id
		FROM ai_viral_clips c
		JOIN episodes e ON e.id = c.episode_id
		JOIN series_fleet s ON s.id = e.series_id
		WHERE c.is_posted = TRUE
		  AND c.hub_message_id IS NOT NULL
		  AND c.created_at >= NOW() - INTERVAL '24 hours'
		ORDER BY s.hub_channel_id, c.viral_score DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	topClips := make(map[int64]int64)
	for rows.Next() {
		var hubID, msgID int64
		if err := rows.Scan(&hubID, &msgID); err == nil {
			topClips[hubID] = msgID
		}
	}
	return topClips, nil
}

