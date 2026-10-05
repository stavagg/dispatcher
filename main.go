package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type Reminder struct {
	ChatID int    `json:"chat_id"`
	Text   string `json:"text"`
	SendAt string `json:"send_at"`
}

var (
	store   []Reminder
	storeMu sync.Mutex
)

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	var rem Reminder

	err := json.NewDecoder(r.Body).Decode(&rem)
	if err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if rem.Text == "" || rem.ChatID == 0 {
		http.Error(w, "text and chat_id required", http.StatusBadRequest)
		return
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	store = append(store, rem)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(rem)
}

func handleListSchedule(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	storeMu.Lock()
	defer storeMu.Unlock()
	json.NewEncoder(w).Encode(store)
}

func main() {

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handleHealthz) ///
	mux.HandleFunc("POST /schedule", handleCreateSchedule)
	mux.HandleFunc("GET /schedule", handleListSchedule)

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("starting http server", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutdown signal received, draining...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	log.Info("bye")
}
