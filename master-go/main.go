package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	cfg := LoadConfig()
	log.Printf("[TeleFleet] Çoklu Kanal Master servisi başlatılıyor (Port: %s, Zorunlu Kanallar: %v)...", cfg.WebServerPort, cfg.RequiredChannels)

	var repo *Repository
	var err error
	for attempt := 1; attempt <= 10; attempt++ {
		repo, err = NewRepository(cfg.DatabaseURL)
		if err == nil {
			break
		}
		log.Printf("[TeleFleet] Veritabanı bağlantısı bekleniyor (%d/10): %v", attempt, err)
		time.Sleep(3 * time.Second)
	}
	if err != nil {
		log.Fatalf("[TeleFleet] Veritabanına bağlanılamadı: %v", err)
	}
	defer repo.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fleetMaster := NewFleetMaster(cfg, repo)
	scheduler := NewFleetScheduler(fleetMaster, repo)
	webPlayer := NewWebPlayerServer(cfg, repo)

	go fleetMaster.StartPollingUpdates(ctx)
	go scheduler.Start(ctx)

	mux := http.NewServeMux()
	webPlayer.RegisterRoutes(mux)

	srv := &http.Server{
		Addr:         ":" + cfg.WebServerPort,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("[TeleFleet] Web Player ve API sunucusu dinleniyor: http://0.0.0.0:%s/watch", cfg.WebServerPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[TeleFleet] HTTP sunucu hatası: %v", err)
		}
	}()

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	<-stopChan

	log.Println("[TeleFleet] Kapatma sinyali alındı, servisler durduruluyor...")
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}

