package api

import "time"

type Event struct {
	ID        int64     `json:"id"`
	ThreadID  string    `json:"thread_id"`
	Type      string    `json:"type"`
	Data      string    `json:"data"`
	CreatedAt time.Time `json:"created_at"`
}
