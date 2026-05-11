package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	r := chi.NewRouter()
	r.Handle("/metrics", promhttp.Handler())
	http.ListenAndServe(":8080", r)
}
