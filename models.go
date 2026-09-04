package main

import "time"

type Config struct {
	MemoryDir    string
	GlobalConfig GlobalConfig
	ProjectRoot  string
	// ScopeResolved is true when --project was used to resolve the memory
	// dir via the registry. When true, command handlers should NOT also
	// filter by project name (the scope already narrows to the right memories).
	ScopeResolved bool
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

	// Verification state, written only by `verify --mark`.
	Stale    bool      `json:"stale,omitempty"`
	Verified time.Time `json:"verified,omitempty"`

	// Supersession state. A superseded memory is never rewritten and never
	// deleted: it keeps its content and gains a forward pointer, so the
	// belief it recorded stays readable alongside the one that replaced it.
	SupersededBy     string    `json:"superseded_by,omitempty"`
	SupersededAt     time.Time `json:"superseded_at,omitempty"`
	SupersededReason string    `json:"superseded_reason,omitempty"`
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

	Stale        bool   `json:"stale,omitempty"`
	SupersededBy string `json:"superseded_by,omitempty"`
}

type SearchOptions struct {
	Project string
	Session string
	Tags    []string
	TagOnly bool
	Weights SearchWeights
}

// Claim is one externally checkable assertion extracted from a memory's text:
// a filesystem path, a URL, or a code repository.
type Claim struct {
	Kind    string `json:"kind"`    // path | url | repo
	Value   string `json:"value"`
	Verdict string `json:"verdict"` // fresh | stale | unknown
	Detail  string `json:"detail,omitempty"`
}

// VerifyResult is one memory's verdict: the worst verdict among its claims.
type VerifyResult struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Project string  `json:"project"`
	Verdict string  `json:"verdict"` // fresh | stale | unknown | no_claims
	Created string  `json:"created"`
	Marked  bool    `json:"marked,omitempty"`
	Claims  []Claim `json:"claims,omitempty"`
}

// LedgerEntry is one append-only record of a governance action. The snapshot
// is what makes the ledger outlive its subject: a deleted memory still has a
// readable body here.
type LedgerEntry struct {
	Timestamp string          `json:"ts"`
	Action    string          `json:"action"` // supersede | delete
	Actor     string          `json:"actor"`
	MemoryID  string          `json:"memory_id"`
	Target    string          `json:"superseded_by,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	Snapshot  *LedgerSnapshot `json:"snapshot,omitempty"`
}

type LedgerSnapshot struct {
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type,omitempty"`
	Project     string   `json:"project,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Created     string   `json:"created,omitempty"`
	Content     string   `json:"content,omitempty"`
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
