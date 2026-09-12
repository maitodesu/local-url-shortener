package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"prod-url-shortener/encode"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var pool *pgxpool.Pool = nil
var rdb *redis.Client = nil

func databaseURL() string {
	var user = os.Getenv("POSTGRES_USER")
	var password = os.Getenv("POSTGRES_PASSWORD")
	var hostname = os.Getenv("POSTGRES_HOSTNAME")
	var port = os.Getenv("POSTGRES_PORT")
	var db = os.Getenv("POSTGRES_DB")

	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, password, hostname, port, db)
}

type UserLink struct {
	URL string `json:"url"`
}

func pingHandler(w http.ResponseWriter, r *http.Request) {
	log.Print(r.Method)
	if _, err := fmt.Fprintln(w, "Status healthy!"); err != nil {
		http.Error(w, "Couldn't ping", http.StatusBadRequest)
		return
	}
}

func codeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	code := r.PathValue("code")
	var longURL string
	var err error
	var sql string
	longURL, err = rdb.Get(r.Context(), code).Result()
	if err != nil {
		// if it's behaving bad, we log and still continue as DB might working, we don't want to stop the functionality
		if !errors.Is(err, redis.Nil) {
			log.Print("Redis is not working, check please")

		}
		// if not found we still continue to DB

		sql = "SELECT long_url FROM LINKS WHERE code = $1"
		if err := pool.QueryRow(r.Context(), sql, code).Scan(&longURL); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "Link not found"})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Fetching failed"})
			return
		}
	}
	sql = "UPDATE links SET hit_count = hit_count + 1 WHERE code = $1"
	// make this non fatal, we take this in our design that it's okay to have this hit count non consistent because of db failures, but user should go to their respective URL
	if _, err := pool.Exec(r.Context(), sql, code); err != nil {
		log.Print("Couldn't update the hit count for ", longURL)
	}

	if errors.Is(err, redis.Nil) {
		rdb.Set(r.Context(), code, longURL, 0)
	}

	http.Redirect(w, r, longURL, http.StatusFound)
}

func shortenHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var link UserLink
	if err := json.NewDecoder(r.Body).Decode(&link); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "JSON is invalid"})
		return
	}

	ctx := r.Context()
	transaction, err := pool.Begin(ctx)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "DB issue"})
		return
	}
	defer transaction.Rollback(ctx)
	sql := "INSERT INTO links (long_url) VALUES ($1) RETURNING id"
	var id int64

	if strings.TrimSpace(link.URL) == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Empty JSON brother"})
		return
	}
	if err := transaction.QueryRow(ctx, sql, link.URL).Scan(&id); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Link addition failed"})
		return
	}
	encoded_id := encode.Encode(id)
	if _, err := transaction.Exec(ctx, "UPDATE links SET code = $1 WHERE id = $2", encoded_id, id); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Link update failed"})
		return
	}

	if err := transaction.Commit(ctx); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "DB commit failed"})
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"code": encoded_id})
}

func main() {
	http.HandleFunc("POST /short", shortenHandler)
	http.HandleFunc("GET /{code}", codeHandler)
	http.HandleFunc("GET /ping", pingHandler)
	ctx := context.Background()
	var err error
	pool, err = pgxpool.New(ctx, databaseURL())
	if err != nil {
		log.Fatal("Error connecting to DB", err)
	}
	if err = pool.Ping(ctx); err != nil {
		log.Fatal("Error pinging DB", err)
	}
	rdb = redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", os.Getenv("REDIS_HOST"), os.Getenv("REDIS_PORT")),
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       0,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal("Error pinging redis", err)
	}
	log.Fatal(http.ListenAndServe(":8080", nil))
}
