package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type FleetMaster struct {
	cfg        FleetConfig
	repo       *Repository
	httpClient *http.Client
}

type tgUpdateResponse struct {
	Ok     bool       `json:"ok"`
	Result []tgUpdate `json:"result"`
}

type tgUpdate struct {
	UpdateID          int64                `json:"update_id"`
	Message           *tgMessage           `json:"message"`
	CallbackQuery     *tgCallbackQuery     `json:"callback_query"`
	PreCheckoutQuery  *tgPreCheckoutQuery  `json:"pre_checkout_query"`
}

type tgMessage struct {
	MessageID         int64                `json:"message_id"`
	From              *tgUser              `json:"from"`
	Chat              tgChat               `json:"chat"`
	Text              string               `json:"text"`
	SuccessfulPayment *tgSuccessfulPayment `json:"successful_payment"`
}

type tgUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type tgChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type tgCallbackQuery struct {
	ID      string     `json:"id"`
	From    tgUser     `json:"from"`
	Message *tgMessage `json:"message"`
	Data    string     `json:"data"`
}

type tgPreCheckoutQuery struct {
	ID             string `json:"id"`
	From           tgUser `json:"from"`
	Currency       string `json:"currency"`
	TotalAmount    int    `json:"total_amount"`
	InvoicePayload string `json:"invoice_payload"`
}

type tgSuccessfulPayment struct {
	Currency                string `json:"currency"`
	TotalAmount             int    `json:"total_amount"`
	InvoicePayload          string `json:"invoice_payload"`
	TelegramPaymentChargeID string `json:"telegram_payment_charge_id"`
}

func NewFleetMaster(cfg FleetConfig, repo *Repository) *FleetMaster {
	return &FleetMaster{
		cfg:  cfg,
		repo: repo,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (fm *FleetMaster) apiURL(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", fm.cfg.LocalBotAPIServer, fm.cfg.BotToken, method)
}

// CheckUserSubscribed kullanıcının zorunlu kanallara abone olup olmadığını kontrol eder.
func (fm *FleetMaster) CheckUserSubscribed(userID int64) (bool, []string) {
	if fm.cfg.BotToken == "" {
		return true, nil
	}
	var missing []string
	for _, ch := range fm.cfg.RequiredChannels {
		q := url.Values{}
		q.Set("chat_id", ch)
		q.Set("user_id", fmt.Sprintf("%d", userID))
		endpoint := fm.apiURL("getChatMember") + "?" + q.Encode()

		resp, err := fm.httpClient.Get(endpoint)
		if err != nil {
			missing = append(missing, ch)
			continue
		}

		var result struct {
			Ok     bool `json:"ok"`
			Result struct {
				Status string `json:"status"`
			} `json:"result"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		st := result.Result.Status
		if !result.Ok || (st != "member" && st != "administrator" && st != "creator") {
			missing = append(missing, ch)
		}
	}
	return len(missing) == 0, missing
}

// PostViralClipToHub 9:16 dikey kesiti ilgili dizinin Hub kanalına gönderir.
func (fm *FleetMaster) PostViralClipToHub(hubChannelID int64, clipLocalPath, captionRU, episodeCode string) (int64, error) {
	deepLink := fmt.Sprintf("https://t.me/%s?start=%s", fm.cfg.BotUsername, episodeCode)
	replyMarkup := map[string]interface{}{
		"inline_keyboard": [][]map[string]string{
			{
				{
					"text": "▶️ Смотреть полную серию (1080p Русс. Суб)",
					"url":  deepLink,
				},
			},
		},
	}
	markupBytes, _ := json.Marshal(replyMarkup)

	videoRef := clipLocalPath
	if !strings.HasPrefix(videoRef, "file://") && !strings.HasPrefix(videoRef, "http") {
		videoRef = "file://" + clipLocalPath
	}

	payload := map[string]interface{}{
		"chat_id":            hubChannelID,
		"video":              videoRef,
		"caption":            captionRU,
		"supports_streaming": true,
		"reply_markup":       string(markupBytes),
	}
	body, _ := json.Marshal(payload)

	resp, err := fm.httpClient.Post(fm.apiURL("sendVideo"), "application/json", bytes.NewBuffer(body))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var res struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&res)
	if !res.Ok {
		return 0, fmt.Errorf("sendVideo hatası: %s", res.Description)
	}
	log.Printf("[FleetMaster] Viral kesit Hub kanalına (%d) gönderildi: msg_id=%d, ep=%s", hubChannelID, res.Result.MessageID, episodeCode)
	return res.Result.MessageID, nil
}

// UploadFullEpisodeToSpoke 2GB'a kadar tam bölümü Local Bot API üzerinden Spoke kanalına yükler.
func (fm *FleetMaster) UploadFullEpisodeToSpoke(spokeChannelID int64, filePath, titleRU string, epNumber int) (int64, error) {
	videoRef := filePath
	if !strings.HasPrefix(videoRef, "file://") && !strings.HasPrefix(videoRef, "http") {
		videoRef = "file://" + filePath
	}

	caption := fmt.Sprintf("🎬 **%s — %d Серия (Full HD 1080p)**\n🇷🇺 Русские субтитры", titleRU, epNumber)
	payload := map[string]interface{}{
		"chat_id":            spokeChannelID,
		"video":              videoRef,
		"caption":            caption,
		"parse_mode":         "Markdown",
		"supports_streaming": true,
	}
	body, _ := json.Marshal(payload)

	resp, err := fm.httpClient.Post(fm.apiURL("sendVideo"), "application/json", bytes.NewBuffer(body))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var res struct {
		Ok          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&res)
	if !res.Ok {
		return 0, fmt.Errorf("Spoke sendVideo hatası: %s", res.Description)
	}
	return res.Result.MessageID, nil
}

// HandleStartCommand kullanıcı "Tam Bölümü İzle" butonundan bota geldiğinde çalışır.
func (fm *FleetMaster) HandleStartCommand(chatID int64, user tgUser, episodeCode string) {
	var seriesIDPtr *string
	if ep, err := fm.repo.GetEpisodeByCode(episodeCode); err == nil && ep != nil {
		seriesIDPtr = &ep.SeriesID
	}
	_ = fm.repo.UpsertTelegramUser(user.ID, user.Username, user.FirstName, seriesIDPtr)

	// Kullanıcı VIP ise doğrudan reklamsız erişim sun
	if isVIP, err := fm.repo.IsUserVIP(user.ID); err == nil && isVIP {
		fm.sendVIPDirectAccess(chatID, user.ID, episodeCode)
		return
	}

	isSubbed, missingChannels := fm.CheckUserSubscribed(user.ID)
	if !isSubbed {
		var buttons [][]map[string]string
		for idx, ch := range missingChannels {
			cleanName := strings.TrimPrefix(ch, "@")
			buttons = append(buttons, []map[string]string{
				{
					"text": fmt.Sprintf("📢 Подписаться на Канал #%d (%s)", idx+1, ch),
					"url":  "https://t.me/" + cleanName,
				},
			})
		}
		buttons = append(buttons, []map[string]string{
			{
				"text":          "✅ Я подписался — Открыть серию!",
				"callback_data": "verify_" + episodeCode,
			},
		})

		fm.sendMessageWithKeyboard(
			chatID,
			"🔒 *Доступ к полной серии ограничен!*\n\nЧтобы смотреть серию бесплатно в Full HD, подпишитесь на наши основные каналы ниже и нажмите кнопку проверки:",
			buttons,
		)
		return
	}

	playerURL := fmt.Sprintf("%s?ep=%s&uid=%d", fm.cfg.WebPlayerBaseURL, url.QueryEscape(episodeCode), user.ID)
	buttons := [][]map[string]string{
		{
			{"text": "🎬 Смотреть серию в Плеере (Бесплатно)", "url": playerURL},
		},
		{
			{
				"text":          fmt.Sprintf("⚡ Открыть без рекламы в Telegram (%d ⭐️ VIP)", fm.cfg.VIPPriceStars),
				"callback_data": "vip_" + episodeCode,
			},
		},
	}

	fm.sendMessageWithKeyboard(
		chatID,
		"✅ *Подписка подтверждена!*\n\nВыберите удобный способ просмотра полной серии:",
		buttons,
	)
}

func (fm *FleetMaster) sendVIPDirectAccess(chatID, userID int64, episodeCode string) {
	playerURL := fmt.Sprintf("%s?ep=%s&uid=%d&vip=1", fm.cfg.WebPlayerBaseURL, url.QueryEscape(episodeCode), userID)
	var buttons [][]map[string]string
	buttons = append(buttons, []map[string]string{
		{"text": "⚡ Смотреть в 4K/1080p Плеере (Без рекламы)", "url": playerURL},
	})
	if ep, err := fm.repo.GetEpisodeByCode(episodeCode); err == nil && ep != nil && ep.ActiveSpokeInviteLink != "" {
		buttons = append(buttons, []map[string]string{
			{"text": "📂 Открыть в закрытом Telegram-архиве", "url": ep.ActiveSpokeInviteLink},
		})
	}
	fm.sendMessageWithKeyboard(
		chatID,
		"👑 *VIP-доступ активен!*\n\nПриятного просмотра без ожидания и рекламы:",
		buttons,
	)
}

// SendStarsVIPInvoice Telegram Stars (XTR) üzerinden tek tıkla VIP abonelik faturası gönderir.
func (fm *FleetMaster) SendStarsVIPInvoice(chatID int64, episodeCode string) {
	payload := map[string]interface{}{
		"chat_id":        chatID,
		"title":          "⚡ VIP-Доступ без рекламы (30 дней)",
		"description":    "Мгновенный доступ ко всем турецким сериалам в Full HD без ожидания и рекламы на 30 дней.",
		"payload":        "vip_stars_" + episodeCode,
		"provider_token": "", // Telegram Stars (XTR) için boş bırakılır
		"currency":       "XTR",
		"prices": []map[string]interface{}{
			{
				"label":  fmt.Sprintf("VIP на %d дней", fm.cfg.VIPDurationDays),
				"amount": fm.cfg.VIPPriceStars,
			},
		},
	}
	body, _ := json.Marshal(payload)
	_, _ = fm.httpClient.Post(fm.apiURL("sendInvoice"), "application/json", bytes.NewBuffer(body))
}

func (fm *FleetMaster) sendMessageWithKeyboard(chatID int64, text string, keyboard [][]map[string]string) {
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	if len(keyboard) > 0 {
		markup, _ := json.Marshal(map[string]interface{}{"inline_keyboard": keyboard})
		payload["reply_markup"] = string(markup)
	}
	body, _ := json.Marshal(payload)
	_, _ = fm.httpClient.Post(fm.apiURL("sendMessage"), "application/json", bytes.NewBuffer(body))
}

func (fm *FleetMaster) answerCallbackQuery(callbackID, text string) {
	payload, _ := json.Marshal(map[string]interface{}{
		"callback_query_id": callbackID,
		"text":              text,
	})
	_, _ = fm.httpClient.Post(fm.apiURL("answerCallbackQuery"), "application/json", bytes.NewBuffer(payload))
}

func (fm *FleetMaster) answerPreCheckoutQuery(queryID string, ok bool) {
	payload, _ := json.Marshal(map[string]interface{}{
		"pre_checkout_query_id": queryID,
		"ok":                    ok,
	})
	_, _ = fm.httpClient.Post(fm.apiURL("answerPreCheckoutQuery"), "application/json", bytes.NewBuffer(payload))
}

// StartPollingUpdates Telegram güncellemelerini (komutlar, butonlar, Stars ödemeleri) dinler.
func (fm *FleetMaster) StartPollingUpdates(ctx context.Context) {
	if fm.cfg.BotToken == "" {
		log.Println("[FleetMaster] TELEGRAM_BOT_TOKEN tanımlanmadı, bot polling devre dışı.")
		return
	}
	log.Println("[FleetMaster] Telegram Bot polling başlatıldı...")
	var offset int64 = 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		q := url.Values{}
		q.Set("offset", fmt.Sprintf("%d", offset))
		q.Set("timeout", "25")
		resp, err := fm.httpClient.Get(fm.apiURL("getUpdates") + "?" + q.Encode())
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		var updates tgUpdateResponse
		_ = json.NewDecoder(resp.Body).Decode(&updates)
		resp.Body.Close()

		for _, u := range updates.Result {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			fm.handleSingleUpdate(u)
		}
	}
}

func (fm *FleetMaster) handleSingleUpdate(u tgUpdate) {
	if u.PreCheckoutQuery != nil {
		fm.answerPreCheckoutQuery(u.PreCheckoutQuery.ID, true)
		return
	}

	if u.Message != nil {
		msg := u.Message
		if msg.SuccessfulPayment != nil && msg.From != nil {
			_ = fm.repo.ActivateUserVIP(msg.From.ID, fm.cfg.VIPDurationDays)
			epCode := strings.TrimPrefix(msg.SuccessfulPayment.InvoicePayload, "vip_stars_")
			fm.sendVIPDirectAccess(msg.Chat.ID, msg.From.ID, epCode)
			return
		}

		if strings.HasPrefix(msg.Text, "/start") && msg.From != nil {
			parts := strings.Fields(msg.Text)
			epCode := "default"
			if len(parts) > 1 {
				epCode = parts[1]
			}
			fm.HandleStartCommand(msg.Chat.ID, *msg.From, epCode)
		}
		return
	}

	if u.CallbackQuery != nil {
		cb := u.CallbackQuery
		data := cb.Data
		chatID := cb.From.ID
		if cb.Message != nil {
			chatID = cb.Message.Chat.ID
		}

		if strings.HasPrefix(data, "verify_") {
			epCode := strings.TrimPrefix(data, "verify_")
			fm.answerCallbackQuery(cb.ID, "Проверяем подписку...")
			fm.HandleStartCommand(chatID, cb.From, epCode)
		} else if strings.HasPrefix(data, "vip_") {
			epCode := strings.TrimPrefix(data, "vip_")
			fm.answerCallbackQuery(cb.ID, "Формируем счет Telegram Stars...")
			fm.SendStarsVIPInvoice(chatID, epCode)
		}
	}
}

