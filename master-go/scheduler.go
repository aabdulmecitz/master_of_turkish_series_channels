package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

type FleetScheduler struct {
	fm   *FleetMaster
	repo *Repository
}

func NewFleetScheduler(fm *FleetMaster, repo *Repository) *FleetScheduler {
	return &FleetScheduler{
		fm:   fm,
		repo: repo,
	}
}

func (s *FleetScheduler) Start(ctx context.Context) {
	clipTicker := time.NewTicker(30 * time.Second)
	spokeHealthTicker := time.NewTicker(2 * time.Minute)
	crossPromoCleanupTicker := time.NewTicker(10 * time.Minute)
	crossPromoDailyTicker := time.NewTicker(1 * time.Hour)

	defer clipTicker.Stop()
	defer spokeHealthTicker.Stop()
	defer crossPromoCleanupTicker.Stop()
	defer crossPromoDailyTicker.Stop()

	var lastCrossPromoDay int = -1

	log.Println("[Scheduler] Zamanlayıcı servisleri (Hub Clip Poster, Spoke Failover, Cross-Promo) aktif.")

	for {
		select {
		case <-ctx.Done():
			return
		case <-clipTicker.C:
			s.processScheduledClips()
			s.processUploadingEpisodes()
		case <-spokeHealthTicker.C:
			s.checkSpokeChannelsAndFailover()
		case <-crossPromoCleanupTicker.C:
			s.cleanupExpiredCrossPromos()
		case <-crossPromoDailyTicker.C:
			now := time.Now().UTC()
			// Her akşam saat 20:00 (UTC+3 için 17:00 UTC) çapraz tanıtım çalıştır
			if now.Hour() == 17 && now.YearDay() != lastCrossPromoDay {
				lastCrossPromoDay = now.YearDay()
				s.executeCrossPromoMatrix()
			}
		}
	}
}

func (s *FleetScheduler) processScheduledClips() {
	if s.fm.cfg.BotToken == "" {
		return
	}
	clips, err := s.repo.FetchDueViralClips(10)
	if err != nil {
		log.Printf("[Scheduler] Kesitler sorgulanırken hata: %v", err)
		return
	}
	for _, clip := range clips {
		msgID, err := s.fm.PostViralClipToHub(
			clip.HubChannelID,
			clip.ClipFilePath,
			clip.CaptionRU,
			clip.EpisodeCode,
		)
		if err != nil {
			log.Printf("[Scheduler] Kesit gönderilemedi (clip=%s): %v", clip.ClipID, err)
			continue
		}
		if err := s.repo.MarkClipPosted(clip.ClipID, msgID); err != nil {
			log.Printf("[Scheduler] Kesit durumu güncellenemedi: %v", err)
		}
	}
}

func (s *FleetScheduler) processUploadingEpisodes() {
	episodes, err := s.repo.FetchUploadingEpisodes(5)
	if err != nil {
		return
	}
	for _, ep := range episodes {
		var msgID int64 = 0
		if s.fm.cfg.BotToken != "" && ep.RawFilePath != "" && ep.ActiveSpokeChannelID != 0 {
			uploadedID, err := s.fm.UploadFullEpisodeToSpoke(
				ep.ActiveSpokeChannelID,
				ep.RawFilePath,
				ep.TitleRU,
				ep.EpisodeNumber,
			)
			if err != nil {
				log.Printf("[Scheduler] Spoke kanalına yükleme uyarısı (%s): %v", ep.EpisodeCode, err)
			} else {
				msgID = uploadedID
			}
		}
		_ = s.repo.MarkEpisodePublished(ep.ID, msgID)
	}
}

// checkSpokeChannelsAndFailover aktif Spoke kanalının erişilebilirliğini kontrol eder;
// kanal erişilemez durumdaysa backup_spoke_channel_id kanalını devreye alır.
func (s *FleetScheduler) checkSpokeChannelsAndFailover() {
	if s.fm.cfg.BotToken == "" {
		return
	}
	fleet, err := s.repo.FetchActiveSeriesFleet()
	if err != nil {
		return
	}

	for _, series := range fleet {
		if !s.isChannelHealthy(series.ActiveSpokeChannelID) && series.BackupSpokeChannelID.Valid {
			backupID := series.BackupSpokeChannelID.Int64
			newInvite, err := s.createChannelInviteLink(backupID)
			if err != nil {
				log.Printf("[SpokeFailover] Yedek kanal davet linki oluşturulamadı (%s): %v", series.Slug, err)
				continue
			}
			if err := s.repo.SwitchToBackupSpoke(series.ID, backupID, newInvite); err == nil {
				log.Printf("[SpokeFailover] %s dizisi için yedek Spoke kanalı (%d) devreye alındı! Yeni link: %s", series.Slug, backupID, newInvite)
			}
		}
	}
}

func (s *FleetScheduler) isChannelHealthy(channelID int64) bool {
	payload, _ := json.Marshal(map[string]interface{}{"chat_id": channelID})
	resp, err := s.fm.httpClient.Post(s.fm.apiURL("getChat"), "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return true // Ağ hatasında yanlış failover yapma
	}
	defer resp.Body.Close()

	var res struct {
		Ok bool `json:"ok"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&res)
	return res.Ok
}

func (s *FleetScheduler) createChannelInviteLink(channelID int64) (string, error) {
	payload, _ := json.Marshal(map[string]interface{}{
		"chat_id": fmt.Sprintf("%d", channelID),
		"name":    "AutoFailoverLink",
	})
	resp, err := s.fm.httpClient.Post(s.fm.apiURL("createChatInviteLink"), "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var res struct {
		Ok     bool `json:"ok"`
		Result struct {
			InviteLink string `json:"invite_link"`
		} `json:"result"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&res)
	if !res.Ok || res.Result.InviteLink == "" {
		return "", fmt.Errorf("createChatInviteLink başarısız")
	}
	return res.Result.InviteLink, nil
}

// executeCrossPromoMatrix Hub kanalları arasında en yüksek skorlu kesitleri çapraz paylaşır
// ve 6 saat sonra otomatik silinmek üzere kaydeder.
func (s *FleetScheduler) executeCrossPromoMatrix() {
	if s.fm.cfg.BotToken == "" {
		return
	}
	topClips, err := s.repo.FetchTopClipPerHubChannel()
	if err != nil || len(topClips) < 2 {
		return
	}

	var channels []int64
	for chID := range topClips {
		channels = append(channels, chID)
	}

	deleteAfter := time.Now().UTC().Add(6 * time.Hour)
	for i := 0; i < len(channels); i++ {
		sourceCh := channels[i]
		targetCh := channels[(i+1)%len(channels)]
		sourceMsgID := topClips[sourceCh]

		fwdID, err := s.forwardMessage(sourceCh, targetCh, sourceMsgID)
		if err == nil && fwdID > 0 {
			_ = s.repo.LogCrossPromoForward(sourceCh, sourceMsgID, targetCh, fwdID, deleteAfter)
			log.Printf("[CrossPromo] Kanal %d -> Kanal %d mesaj (%d) forward edildi (6 saat sonra silinecek).", sourceCh, targetCh, fwdID)
		}
	}
}

func (s *FleetScheduler) forwardMessage(fromChatID, toChatID, messageID int64) (int64, error) {
	payload, _ := json.Marshal(map[string]interface{}{
		"chat_id":      toChatID,
		"from_chat_id": fromChatID,
		"message_id":   messageID,
	})
	resp, err := s.fm.httpClient.Post(s.fm.apiURL("forwardMessage"), "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var res struct {
		Ok     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&res)
	if !res.Ok {
		return 0, fmt.Errorf("forwardMessage başarısız")
	}
	return res.Result.MessageID, nil
}

func (s *FleetScheduler) cleanupExpiredCrossPromos() {
	if s.fm.cfg.BotToken == "" {
		return
	}
	expired, err := s.repo.FetchExpiredCrossPromos()
	if err != nil {
		return
	}
	for _, item := range expired {
		payload, _ := json.Marshal(map[string]interface{}{
			"chat_id":    item.TargetChannelID,
			"message_id": item.ForwardedMessageID,
		})
		resp, err := s.fm.httpClient.Post(s.fm.apiURL("deleteMessage"), "application/json", bytes.NewBuffer(payload))
		if err == nil {
			resp.Body.Close()
		}
		_ = s.repo.MarkCrossPromoDeleted(item.ID)
	}
}

