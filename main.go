package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"prod-url-shortener/encode"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var pool *pgxpool.Pool
var rdb *redis.Client

var luaScript []byte

const maxTokens int8 = 60
const refillRate float32 = 0.06 // tokens per second (~1 token every 16.7s)

func getClientIP(r *http.Request) string {
	// Only trust X-Forwarded-For when explicitly told we're behind our own
	// reverse proxy (which overwrites this header with the real client IP,
	// rather than trusting whatever a client sent). Without this flag, a
	// directly-reachable instance would let any client spoof its IP via
	// this same header, so it defaults off.
	if os.Getenv("TRUST_PROXY_HEADERS") == "true" {
		if xForwardedFor := r.Header.Get("X-Forwarded-For"); xForwardedFor != "" {
			return strings.TrimSpace(xForwardedFor)
		}
	}

	// RemoteAddr (strip the port number)
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr // Fallback if splitting fails
	}
	return ip
}

func rateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		proceed := rateLimitChecker(r)
		if proceed {
			next(w, r)
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{"error": "yamete kudasai"})
	}
}

// Return a bool whether a request is allowed for a ip or not,
// via a fixed bucket based approach using LUA in redis
func rateLimitChecker(r *http.Request) bool {
	ip := getClientIP(r)
	cmd := rdb.Eval(r.Context(), string(luaScript), []string{fmt.Sprintf("ratelimit:%s", ip)}, maxTokens, refillRate, time.Now().Unix())
	result, err := cmd.Int64()
	if err != nil {
		// Handle Redis error (e.g., redis connection lost or script compilation failure)
		log.Printf("Redis error: %v", err)
		return false
	}

	return result == 1
}

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

// resolveLongURL looks up a code's destination via cache-aside: Redis first,
// Postgres on a miss, populating the cache before returning. Shared by
// codeHandler (redirect) and previewHandler (needs the same destination to
// hand to the preview API), so both stay in sync with one lookup path.
func resolveLongURL(ctx context.Context, code string) (string, error) {
	longURL, err := rdb.Get(ctx, fmt.Sprintf("url:%s", code)).Result()
	if err == nil {
		return longURL, nil
	}
	if !errors.Is(err, redis.Nil) {
		log.Print("Redis is not working, check please")
	}

	sql := "SELECT long_url FROM links WHERE code = $1"
	if err := pool.QueryRow(ctx, sql, code).Scan(&longURL); err != nil {
		return "", err
	}
	rdb.Set(ctx, fmt.Sprintf("url:%s", code), longURL, 0)
	return longURL, nil
}

func codeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	code := r.PathValue("code")

	longURL, err := resolveLongURL(r.Context(), code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "Link not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Fetching failed"})
		return
	}

	sql := "UPDATE links SET hit_count = hit_count + 1 WHERE code = $1"
	// make this non fatal, we take this in our design that it's okay to have this hit count non consistent because of db failures, but user should go to their respective URL
	if _, err := pool.Exec(r.Context(), sql, code); err != nil {
		log.Print("Couldn't update the hit count for ", longURL)
	}

	http.Redirect(w, r, longURL, http.StatusFound)
}

// previewHandler returns Open Graph-style preview data (title, description,
// image) for a short code's destination, cache-aside via Redis with the
// LinkPreview.net/Exabase API as the origin on a miss. Deliberately never
// touches Postgres for the preview data itself -- that's third-party-derived
// and ephemeral, unlike the long URL, which is canonical app data.
func previewHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	code := r.PathValue("code")

	previewKey := fmt.Sprintf("preview:%s", code)
	if cached, err := rdb.Get(r.Context(), previewKey).Result(); err == nil {
		w.Write([]byte(cached))
		return
	} else if !errors.Is(err, redis.Nil) {
		log.Print("Redis is not working, check please")
	}

	longURL, err := resolveLongURL(r.Context(), code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "Link not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Fetching failed"})
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	apiURL := fmt.Sprintf("https://api.exabase.io/v2/link?url=%s", url.QueryEscape(longURL))
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, apiURL, nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Preview request failed"})
		return
	}
	req.Header.Set("X-Api-Key", os.Getenv("LINKPREVIEW_API_KEY"))

	resp, err := client.Do(req)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": "Couldn't reach preview service"})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": "Preview service error"})
		return
	}

	rdb.Set(r.Context(), previewKey, body, 7*24*time.Hour)
	w.Write(body)
}

func shortenHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var link UserLink
	if err := json.NewDecoder(r.Body).Decode(&link); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "JSON is invalid"})
		return
	}

	if strings.TrimSpace(link.URL) == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Empty JSON brother"})
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

	// ON CONFLICT DO NOTHING relies on the unique index on long_url: a
	// concurrent request for the same URL blocks on that index's row lock
	// until the first transaction commits, so this can't race like a
	// separate SELECT-then-INSERT check would.
	sql := "INSERT INTO links (long_url) VALUES ($1) ON CONFLICT (long_url) DO NOTHING RETURNING id"
	var id int64
	err = transaction.QueryRow(ctx, sql, link.URL).Scan(&id)

	if errors.Is(err, pgx.ErrNoRows) {
		var existingCode string
		selectSQL := "SELECT code FROM links WHERE long_url = $1"
		if err := transaction.QueryRow(ctx, selectSQL, link.URL).Scan(&existingCode); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Link lookup failed"})
			return
		}
		if err := transaction.Commit(ctx); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "DB commit failed"})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"code": existingCode})
		return
	}
	if err != nil {
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
	http.HandleFunc("POST /short", rateLimitMiddleware(shortenHandler))
	http.HandleFunc("GET /{code}", codeHandler)
	http.HandleFunc("GET /{code}/preview", previewHandler)
	http.HandleFunc("GET /ping", pingHandler)
	ctx := context.Background()
	var err error
	luaScript, err = os.ReadFile("token-script.lua")
	if err != nil {
		log.Fatal("Couldn't load lua script, rate limiter broke")
	}
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
