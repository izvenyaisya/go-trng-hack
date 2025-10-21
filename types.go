package main

import (
	"time"
)

type XY struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Point struct {
	ID         int    `json:"id"`
	Color      string `json:"color"`
	PixelWidth int    `json:"pixel_width"`
	Path       []XY   `json:"path"`
}

type SimulationData struct {
	CanvasWidth  int     `json:"canvas_width"`
	CanvasHeight int     `json:"canvas_height"`
	Iterations   int     `json:"iterations"`
	Points       []Point `json:"points"`
	// Для анимации на клиенте
	Step          float64 `json:"step"`
	MotionLaw     string  `json:"motion_law"`
	Sharpness     float64 `json:"sharpness"`
	Smoothness    float64 `json:"smoothness"`
	SpeedScale    float64 `json:"speed_scale"`
	EntropyMode   string  `json:"entropy_mode"`
	NumPoints     int     `json:"num_points"`
	DefaultColors bool    `json:"default_colors"`
}

type EntropySpec struct {
	Mode   string   `json:"mode"`   // os|jitter|http/mix/repro
	Seed64 int64    `json:"seed64"` // используется только при mode=repro
	HTTP   []string `json:"http"`
}

type MotionSpec struct {
	Law        string  // sine|jerk|spiral|flow
	Sharpness  float64 // 0..2
	Smoothness float64 // 0..2
	SpeedScale float64 // 0..3
}

type GenerateParams struct {
	Count      int // итоговая длина в битах
	CanvasW    int
	CanvasH    int
	Iterations int
	NumPoints  int
	PixelWidth int
	Entropy    EntropySpec
	Motion     MotionSpec
	Step       float64 // шаг времени для симуляции
	Whiten     string  // off|on|hmac
}

type GenerationProvenance struct {
	Entropy      EntropySpec `json:"entropy"`
	Motion       MotionSpec  `json:"motion"`
	Iterations   int         `json:"iterations"`
	NumPoints    int         `json:"num_points"`
	PixelWidth   int         `json:"pixel_width"`
	CanvasW      int         `json:"canvas_w"`
	CanvasH      int         `json:"canvas_h"`
	Step         float64     `json:"step"`
	Whiten       string      `json:"whiten"`
	PerHTTPSeeds []int64     `json:"per_http_seeds,omitempty"`
}

type Transaction struct {
	TxID       string               `json:"tx_id"`
	CreatedAt  time.Time            `json:"created_at"`
	Count      int                  `json:"count"`
	Seed       int64                `json:"seed"` // финальный мастер-сид (воспроизводимость)
	Sim        SimulationData       `json:"simulation"`
	DataHash   string               `json:"data_hash"` // SHA256(JSON симуляции)
	BitsHash   string               `json:"bits_hash"` // SHA256(итоговых бит)
	Published  string               `json:"published"` // HKDF(bitsHash, dataHash, label)
	Provenance GenerationProvenance `json:"provenance"`
}

type Block struct {
	Index     int    `json:"index"`
	Timestamp int64  `json:"timestamp"`
	TxID      string `json:"tx_id"`
	DataHash  string `json:"data_hash"` // published
	PrevHash  string `json:"prev_hash"`
	Hash      string `json:"hash"`
}
