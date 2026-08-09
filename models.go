package main

import "time"

type Config struct {
	MemoryDir    string
	GlobalConfig GlobalConfig
	ProjectRoot  string
}

type SearchWeights struct {
	TFIDF      float64 `json:"tfidf"`
	Phrase     float64 `json:"phrase"`
	Exact      float64 `json:"exact"`
	Recency24h float64 `json:"recency24h"`
	Recency7d  float64 `json:"recency7d"`
	Type       float64 `json:"type"`
	Tag        float64 `json:"tag"`
}

type GlobalConfig struct {
	DefaultMemoryType string        `json:"default_memory_type"`
	MaxMemorySize     int           `json:"max_memory_size"`
	AutoIndex         bool          `json:"auto_index"`
	AutoSyncDir       string        `json:"auto_sync_dir,omitempty"`
	AutoSync          bool          `json:"auto_sync,omitempty"`
	SearchWeights     SearchWeights `json:"search_weights"`
}

type Link struct {
	Target   string `json:"target"`
	Relation string `json:"relation"`
	Value    string `json:"value,omitempty"`
}

// Section represents an addressable [slug] block within a memory's content.
type Section struct {
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Preview   string `json:"preview"`
}

type Memory struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Type        string    `json:"type"`
	Project     string    `json:"project"`
	Session     string    `json:"session"`
	Tags        []string  `json:"tags"`
	Created     time.Time `json:"created"`
	Content     string    `json:"content"`
	FilePath    string    `json:"file_path,omitempty"`
	Links       []Link    `json:"links,omitempty"`
	Sections    []Section `json:"sections,omitempty"`
}

type GraphEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation"`
	Value    string `json:"value,omitempty"`
}

type GraphIndex struct {
	Version int         `json:"version"`
	Nodes   []Memory    `json:"nodes"`
	Edges   []GraphEdge `json:"edges"`
}

type SearchIndex struct {
	Version       int                         // index format version
	TermFreq      map[string]map[string]int   // term -> (memoryID -> frequency)
	TermPositions map[string]map[string][]int // term -> (memoryID -> sorted token positions)
	DocFreq       map[string]int              // term -> document frequency
	DocCount      int                         // total number of documents
	Memories      map[string]Memory           // memoryID -> Memory metadata
}

type SearchResult struct {
	MemoryID   string    `json:"memory_id"`
	Score      float64   `json:"score"`
	Title      string    `json:"title"`
	Content    string    `json:"content"`
	MemoryType string    `json:"memory_type"`
	Project    string    `json:"project"`
	Session    string    `json:"session"`
	Tags       []string  `json:"tags"`
	Created    string    `json:"created"`
	Sections   []Section `json:"sections,omitempty"`
	FilePath   string    `json:"file_path,omitempty"`
}

type SearchOptions struct {
	Project string
	Session string
	Tags    []string
	TagOnly bool
	Weights SearchWeights
}

type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code        int    `json:"code"`
	Type        string `json:"type"`
	Message     string `json:"message"`
	Recoverable bool   `json:"recoverable"`
}

type SuccessResponse struct {
	Version string      `json:"version"`
	Data    interface{} `json:"data"`
}
