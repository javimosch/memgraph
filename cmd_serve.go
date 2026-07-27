package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed ui/*
var uiFS embed.FS

func isClientDisconnect(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)
}

type nodeInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	Project  string    `json:"project"`
	Tags     []string  `json:"tags"`
	Created  time.Time `json:"created"`
	FilePath string    `json:"file_path,omitempty"`
}

type serverState struct {
	mu        sync.RWMutex
	graph     *GraphIndex
	nodeMap   map[string]Memory
	index     *SearchIndex
	cfg       *Config
	syncDirs  []string
	lastSync  time.Time
}

func expandHomeDir(path string) string {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func parseSyncDirs(raw string) []string {
	var dirs []string
	for _, d := range strings.Split(raw, ",") {
		d = strings.TrimSpace(d)
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

func (s *serverState) syncNow() error {
	if len(s.syncDirs) == 0 {
		return nil
	}

	resolvedDirs := make([]string, 0, len(s.syncDirs))
	for _, dir := range s.syncDirs {
		resolvedDir := expandHomeDir(dir)
		if _, err := os.Stat(resolvedDir); os.IsNotExist(err) {
			log.Printf("Sync dir not found, skipping: %s", resolvedDir)
			continue
		}
		resolvedDirs = append(resolvedDirs, resolvedDir)
	}

	if len(resolvedDirs) == 0 {
		return fmt.Errorf("no sync directories found")
	}

	graph, skillCount, namespaceCount, err := ingestMultiDir(resolvedDirs, s.cfg.MemoryDir, s.cfg)
	if err != nil {
		return err
	}

	idx, err := loadSearchIndex(s.cfg.MemoryDir)
	if err != nil {
		idx = s.index
	}

	newGraph, newNodeMap, err := loadGraphForServe(s.cfg.MemoryDir, idx)
	if err != nil {
		newGraph = &GraphIndex{Version: 1, Nodes: graph.Nodes, Edges: graph.Edges}
		newNodeMap = make(map[string]Memory, len(graph.Nodes))
		for _, n := range graph.Nodes {
			newNodeMap[n.ID] = n
		}
	}

	s.mu.Lock()
	s.graph = newGraph
	s.nodeMap = newNodeMap
	s.index = idx
	s.lastSync = time.Now().UTC()
	s.mu.Unlock()

	log.Printf("Sync complete: %d skills, %d namespaces, %d total nodes", skillCount, namespaceCount, len(newGraph.Nodes))
	return nil
}

func (s *serverState) checkAndSync() {
	if len(s.syncDirs) == 0 {
		return
	}

	var latestMod time.Time
	for _, dir := range s.syncDirs {
		resolvedDir := expandHomeDir(dir)
		_ = filepath.WalkDir(resolvedDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if info, err := d.Info(); err == nil {
				if info.ModTime().After(latestMod) {
					latestMod = info.ModTime()
				}
			}
			return nil
		})
	}

	s.mu.RLock()
	last := s.lastSync
	s.mu.RUnlock()

	if latestMod.After(last) {
		if err := s.syncNow(); err != nil {
			log.Printf("Auto-sync error: %v", err)
		}
	}
}

func handleServe(cfg *Config) {
	_, opts := parseCommandArgs(os.Args[2:])
	port := opts.Port
	if port == 0 {
		port = 8080
	}
	addr := fmt.Sprintf("0.0.0.0:%d", port)

	index, err := loadSearchIndex(cfg.MemoryDir)
	if err != nil {
		log.Printf("notice: loadSearchIndex: %v", err)
	}
	graph, nodeMap, err := loadGraphForServe(cfg.MemoryDir, index)
	if err != nil {
		log.Printf("notice: loadGraphForServe: %v", err)
		graph = &GraphIndex{Version: 1}
		nodeMap = make(map[string]Memory)
	}

	syncDirs := []string{}
	if opts.SyncDir != "" {
		syncDirs = parseSyncDirs(opts.SyncDir)
	}
	if len(syncDirs) == 0 && opts.AutoSync {
		syncDirs = []string{"~/.agents/skills", "~/handoffs"}
	}
	if len(syncDirs) == 0 && cfg.GlobalConfig.AutoSyncDir != "" {
		syncDirs = parseSyncDirs(cfg.GlobalConfig.AutoSyncDir)
	}
	if len(syncDirs) == 0 && cfg.GlobalConfig.AutoSync {
		syncDirs = []string{"~/.agents/skills", "~/handoffs"}
	}

	state := &serverState{
		graph:     graph,
		nodeMap:   nodeMap,
		index:     index,
		cfg:       cfg,
		syncDirs:  syncDirs,
		lastSync:  time.Now().UTC().Add(-24 * time.Hour),
	}

	if len(syncDirs) > 0 {
		log.Printf("Auto-sync enabled for: %s", strings.Join(syncDirs, ", "))
		if err := state.syncNow(); err != nil {
			log.Printf("Initial sync warning: %v", err)
		}
		go func() {
			ticker := time.NewTicker(4 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				state.checkAndSync()
			}
		}()
	}

	staticFS, err := fs.Sub(uiFS, "ui")
	if err != nil {
		log.Fatalf("failed to open embedded ui: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		state.mu.RLock()
		g := state.graph
		state.mu.RUnlock()
		apiGraphHandler(w, r, g)
	})
	mux.HandleFunc("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
		state.mu.RLock()
		g := state.graph
		state.mu.RUnlock()
		apiNodesHandler(w, r, g)
	})
	mux.HandleFunc("/api/nodes/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/nodes" || r.URL.Path == "/api/nodes/" {
			state.mu.RLock()
			g := state.graph
			state.mu.RUnlock()
			apiNodesHandler(w, r, g)
			return
		}
		state.mu.RLock()
		nm := state.nodeMap
		state.mu.RUnlock()
		apiNodeHandler(w, r, cfg, nm)
	})
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		state.mu.RLock()
		idx := state.index
		state.mu.RUnlock()
		apiSearchHandler(w, r, cfg, idx)
	})
	mux.HandleFunc("/api/sync", func(w http.ResponseWriter, r *http.Request) {
		if err := state.syncNow(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		state.mu.RLock()
		nodesCount := len(state.graph.Nodes)
		edgesCount := len(state.graph.Edges)
		state.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"nodes":  nodesCount,
			"edges":  edgesCount,
		})
	})
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		serveFileFromFS(w, r, staticFS, "index.html", "text/html; charset=utf-8")
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		log.Printf("Server listening on http://%s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen error: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Println("shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}

func apiGraphHandler(w http.ResponseWriter, r *http.Request, graph *GraphIndex) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(graph); err != nil && !isClientDisconnect(err) {
		log.Printf("failed to encode graph: %v", err)
	}
}

func apiNodesHandler(w http.ResponseWriter, r *http.Request, graph *GraphIndex) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	infos := make([]nodeInfo, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		infos = append(infos, nodeInfo{
			ID:       n.ID,
			Name:     n.Name,
			Type:     n.Type,
			Project:  n.Project,
			Tags:     n.Tags,
			Created:  n.Created,
			FilePath: n.FilePath,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(infos); err != nil && !isClientDisconnect(err) {
		log.Printf("failed to encode nodes: %v", err)
	}
}

func apiNodeHandler(w http.ResponseWriter, r *http.Request, cfg *Config, nodeMap map[string]Memory) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	if id == "" || strings.ContainsAny(id, "/\\") {
		http.NotFound(w, r)
		return
	}
	if _, ok := nodeMap[id]; !ok {
		http.NotFound(w, r)
		return
	}
	for _, name := range []string{"memory_" + id + ".md", id + ".md"} {
		path := filepath.Join(cfg.MemoryDir, name)
		data, err := os.ReadFile(path)
		if err == nil {
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			w.Write(data)
			return
		}
	}
	http.NotFound(w, r)
}

func apiSearchHandler(w http.ResponseWriter, r *http.Request, cfg *Config, index *SearchIndex) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if index == nil {
		http.Error(w, "search index empty", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query().Get("q")
	project := r.URL.Query().Get("project")
	tags := parseTagsValue(r.URL.Query().Get("tags"))
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			limit = n
		}
	}

	opts := SearchOptions{
		Project: project,
		Tags:    tags,
		Weights: cfg.GlobalConfig.SearchWeights,
	}
	results := searchMemories(index, q, opts)
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(results); err != nil && !isClientDisconnect(err) {
		log.Printf("failed to encode search results: %v", err)
	}
}

func serveFileFromFS(w http.ResponseWriter, r *http.Request, staticFS fs.FS, name, contentType string) {
	data, err := fs.ReadFile(staticFS, name)
	if err != nil {
		http.Error(w, name+" missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Write(data)
}
