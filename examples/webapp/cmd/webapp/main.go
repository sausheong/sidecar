package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	promhttp "github.com/prometheus/client_golang/prometheus/promhttp"
	"sidecar-demo/internal/handler"
	"sidecar-demo/internal/metrics"
	"sidecar-demo/internal/middleware"
	"sidecar-demo/internal/store"
)

func main() {
	if err := os.MkdirAll("logs", 0o755); err != nil {
		log.Fatal(err)
	}
	logFile, err := os.OpenFile("logs/app.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer logFile.Close()

	metrics.Register()

	s := store.New()
	h := handler.New(s)

	r := chi.NewRouter()
	r.Use(middleware.Logger(io.MultiWriter(os.Stdout, logFile)))
	r.Use(middleware.Metrics)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	r.Get("/tasks", h.ListTasks)
	r.Post("/tasks", h.CreateTask)
	r.Get("/tasks/{id}", h.GetTask)
	r.Put("/tasks/{id}", h.UpdateTask)
	r.Post("/tasks/{id}/complete", h.CompleteTask)
	r.Delete("/tasks/{id}", h.DeleteTask)
	r.Post("/demo/stress", h.Stress)
	r.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{Addr: ":8080", Handler: r}

	go func() {
		log.Println("listening on :8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}
