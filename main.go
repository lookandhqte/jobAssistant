package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq"
)

type Booking struct {
	ID      int    `json:"id"`
	UserID  int    `json:"user_id"`
	PlaceID int    `json:"place_id"`
	From    string `json:"from"`
	To      string `json:"to"`
}

type BookingsResponse struct {
	Bookings []Booking `json:"bookings"`
}

var db *sql.DB

func main() {
	port := flag.String("port", "8080", "port to listen on")
	flag.Parse()

	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "root")
	dbPass := getEnv("DB_PASSWORD", "root")
	dbName := getEnv("DB_NAME", "contest")

	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		dbHost, dbPort, dbUser, dbPass, dbName)

	var err error
	db, err = sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Ошибка подключения к БД: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("БД недоступна: %v", err)
	}

	// Маршрутизация эндпоинтов
	http.HandleFunc("/ping", handlePing)
	http.HandleFunc("/book", handleBook)
	http.HandleFunc("/booklist", handleBookList)

	log.Printf("Сервис запущен на порту %s", *port)
	if err := http.ListenAndServe(":"+*port, nil); err != nil {
		log.Fatalf("Ошибка запуска сервера: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// GET /ping
func handlePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// POST /book
func handleBook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	placeID, err1 := strconv.Atoi(q.Get("place_id"))
	userID, err2 := strconv.Atoi(q.Get("user_id"))
	fromStr := q.Get("from")
	toStr := q.Get("to")

	if err1 != nil || err2 != nil || fromStr == "" || toStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	fromTime, err3 := time.Parse(time.RFC3339, fromStr)
	toTime, err4 := time.Parse(time.RFC3339, toStr)
	if err3 != nil || err4 != nil || !fromTime.Before(toTime) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Проверка пересечения интервалов:
	// Два полуинтервала [from1, to1) и [from2, to2) пересекаются тогда и только тогда, когда:
	// from1 < to2 И from2 < to1
	var exists bool
	queryCheck := `
		SELECT EXISTS (
			SELECT 1 FROM bookings 
			WHERE place_id = $1 AND time_from < $2 AND time_to > $3
		);`

	err := db.QueryRow(queryCheck, placeID, toTime, fromTime).Scan(&exists)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if exists {
		w.WriteHeader(http.StatusConflict) // 409
		return
	}

	// Создание записи
	queryInsert := `
		INSERT INTO bookings (user_id, place_id, time_from, time_to) 
		VALUES ($1, $2, $3, $4);`

	_, err = db.Exec(queryInsert, userID, placeID, fromTime, toTime)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GET /booklist
func handleBookList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	userIDStr := q.Get("user_id")
	placeIDStr := q.Get("place_id")

	var rows *sql.Rows
	var err error

	// Выборка по user_id или place_id с сортировкой по (time_from, id)
	if userIDStr != "" {
		userID, errParse := strconv.Atoi(userIDStr)
		if errParse != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		query := `
			SELECT id, user_id, place_id, time_from, time_to 
			FROM bookings 
			WHERE user_id = $1 
			ORDER BY time_from ASC, id ASC;`
		rows, err = db.Query(query, userID)
	} else if placeIDStr != "" {
		placeID, errParse := strconv.Atoi(placeIDStr)
		if errParse != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		query := `
			SELECT id, user_id, place_id, time_from, time_to 
			FROM bookings 
			WHERE place_id = $1 
			ORDER BY time_from ASC, id ASC;`
		rows, err = db.Query(query, placeID)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	bookings := []Booking{}
	for rows.Next() {
		var b Booking
		var tFrom, tTo time.Time
		if err := rows.Scan(&b.ID, &b.UserID, &b.PlaceID, &tFrom, &tTo); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Приведение к формату RFC3339 со смещением Z (UTC)
		b.From = tFrom.UTC().Format(time.RFC3339)
		b.To = tTo.UTC().Format(time.RFC3339)
		bookings = append(bookings, b)
	}

	resp := BookingsResponse{Bookings: bookings}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}
