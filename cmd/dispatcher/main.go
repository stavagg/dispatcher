package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Reminder struct {
	ChatID int       `json:"chat_id"`
	Text   string    `json:"text"`
	SendAt time.Time `json:"send_at"`
}

type Server struct {
	db  *pgxpool.Pool
	log *slog.Logger
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var rem Reminder
	//var storeMu sync.Mutex

	err := json.NewDecoder(r.Body).Decode(&rem)

	if err != nil {
		s.log.Error("insert failed", "err", err, "chat_id", rem.ChatID)
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if rem.Text == "" || rem.ChatID == 0 {
		http.Error(w, "text and chat_id required", http.StatusBadRequest)
		return
	}
	//storeMu.Lock()
	//defer storeMu.Unlock()

	_, err = s.db.Exec(r.Context(),
		"INSERT INTO reminders (chat_id, text, send_at) VALUES ($1, $2, $3)",
		rem.ChatID, rem.Text, rem.SendAt)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(rem)
}

func (s *Server) handleListSchedule(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(),
		"SELECT chat_id, text, send_at FROM reminders ORDER BY id")
	if err != nil {
		s.log.Error("query failed", "err", err)
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var reminders []Reminder
	for rows.Next() {
		var rem Reminder
		if err := rows.Scan(&rem.ChatID, &rem.Text, &rem.SendAt); err != nil {
			s.log.Error("row scan failed", "err", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		reminders = append(reminders, rem)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(reminders)
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	pool, err := pgxpool.New(context.Background(), "postgres://postgres:secret@localhost:5432/dispatcher")

	srvHandlers := &Server{db: pool, log: logger}

	if err != nil {
		logger.Error("db connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", srvHandlers.handleHealthz)
	mux.HandleFunc("POST /schedule", srvHandlers.handleCreateSchedule)
	mux.HandleFunc("GET /schedule", srvHandlers.handleListSchedule)

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("starting http server", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received, draining...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	logger.Info("bye")
}
