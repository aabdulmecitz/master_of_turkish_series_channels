package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

type WebPlayerServer struct {
	cfg  FleetConfig
	repo *Repository
}

type PlayerConfigResponse struct {
	EpisodeCode      string              `json:"episode_code"`
	TitleRU          string              `json:"title_ru"`
	EpisodeNumber    int                 `json:"episode_number"`
	IsVIP            bool                `json:"is_vip"`
	StreamURL        string              `json:"stream_url"`
	SpokeInviteLink  string              `json:"spoke_invite_link"`
	BotVIPDeepLink   string              `json:"bot_vip_deep_link"`
	VIPPriceStars    int                 `json:"vip_price_stars"`
	PreRollCampaign  *MonetizationTarget `json:"preroll_campaign,omitempty"`
}

func NewWebPlayerServer(cfg FleetConfig, repo *Repository) *WebPlayerServer {
	return &WebPlayerServer{
		cfg:  cfg,
		repo: repo,
	}
}

func (ws *WebPlayerServer) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", ws.handleHealth)
	mux.HandleFunc("/watch", ws.handleWatchPage)
	mux.HandleFunc("/api/player-config", ws.handlePlayerConfig)
	mux.HandleFunc("/api/ad-complete", ws.handleAdComplete)

	// Yerel medya dosyalarını sunmak için statik dosya sunucusu
	if info, err := os.Stat(ws.cfg.MediaRoot); err == nil && info.IsDir() {
		fs := http.StripPrefix("/media/", http.FileServer(http.Dir(ws.cfg.MediaRoot)))
		mux.Handle("/media/", fs)
	}
}

func (ws *WebPlayerServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","service":"telefleet-master-go"}`))
}

func (ws *WebPlayerServer) handleWatchPage(w http.ResponseWriter, r *http.Request) {
	indexPath := filepath.Join(ws.cfg.WebPlayerDir, "index.html")
	http.ServeFile(w, r, indexPath)
}

func (ws *WebPlayerServer) handlePlayerConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	epCode := r.URL.Query().Get("ep")
	uidStr := r.URL.Query().Get("uid")
	userID, _ := strconv.ParseInt(uidStr, 10, 64)

	isVIP := false
	if userID > 0 {
		if v, err := ws.repo.IsUserVIP(userID); err == nil {
			isVIP = v
		}
	}

	resp := PlayerConfigResponse{
		EpisodeCode:    epCode,
		TitleRU:        "Турецкий Сериал (Full HD 1080p)",
		EpisodeNumber:  1,
		IsVIP:          isVIP,
		StreamURL:      "",
		BotVIPDeepLink: fmt.Sprintf("https://t.me/%s?start=%s", ws.cfg.BotUsername, epCode),
		VIPPriceStars:  ws.cfg.VIPPriceStars,
	}

	if ep, err := ws.repo.GetEpisodeByCode(epCode); err == nil && ep != nil {
		resp.TitleRU = ep.TitleRU
		resp.EpisodeNumber = ep.EpisodeNumber
		resp.StreamURL = ep.StreamURL
		resp.SpokeInviteLink = ep.ActiveSpokeInviteLink
	}

	if !isVIP {
		if target, err := ws.repo.SelectActiveMonetizationTarget("youtube_preroll"); err == nil && target != nil {
			resp.PreRollCampaign = target
		} else {
			// Varsayılan tanıtım / pre-roll yapılandırması
			resp.PreRollCampaign = &MonetizationTarget{
				ID:               "default-preroll",
				CampaignType:     "youtube_preroll",
				Title:            "Спонсорский показ",
				TargetURL:        "M7lc1UVf-VE",
				RequiredWatchSec: 30,
			}
		}
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func (ws *WebPlayerServer) handleAdComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		CampaignID  string `json:"campaign_id"`
		EpisodeCode string `json:"episode_code"`
		UserID      int64  `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.CampaignID != "" && req.CampaignID != "default-preroll" {
		_ = ws.repo.RecordAdCompletion(req.CampaignID, req.UserID)
	}
	log.Printf("[WebServer] Pre-roll tamamlandı: user=%d, ep=%s, campaign=%s", req.UserID, req.EpisodeCode, req.CampaignID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"unlocked": true,
	})
}

