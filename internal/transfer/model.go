// Package transfer synchronizes explicitly selected Engine library entities.
package transfer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	libraryidentity "github.com/alexgorbatchev/engine-cli/internal/identity"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Scope struct {
	Root         bool     `json:"root"`
	PlaylistIDs  []int64  `json:"playlist_ids"`
	PlaylistRefs []string `json:"playlist_refs"`
}
type PlaylistMapping struct {
	SourceID      int64 `json:"source_id"`
	DestinationID int64 `json:"destination_id"`
}
type Options struct {
	Source      string            `json:"source"`
	Destination string            `json:"destination"`
	Scope       Scope             `json:"scope"`
	PlaylistMap []PlaylistMapping `json:"playlist_map"`
	DryRun      bool              `json:"dry_run"`
	Mirror      bool              `json:"mirror"`
	Conflict    string            `json:"conflict"`
	Media       string            `json:"media"`
	PlanToken   string            `json:"plan_token"`
	NoBackup    bool              `json:"-"`
}
type Summary struct {
	BytesRequiredEstimate int64  `json:"bytes_required_estimate"`
	BytesAvailable        uint64 `json:"bytes_available"`
	TracksAdded           int    `json:"tracks_added"`
	TracksReused          int    `json:"tracks_reused"`
	TracksUpdated         int    `json:"tracks_updated"`
	TracksRemoved         int    `json:"tracks_removed"`
	PlaylistsAdded        int    `json:"playlists_added"`
	PlaylistsUpdated      int    `json:"playlists_updated"`
	PlaylistsRemoved      int    `json:"playlists_removed"`
	FilesCopied           int    `json:"files_copied"`
	BytesToCopy           int64  `json:"bytes_to_copy"`
	BytesToPreserve       int64  `json:"bytes_to_preserve"`
	Conflicts             int    `json:"conflicts"`
}
type Change struct {
	Entity        string `json:"entity"`
	ID            string `json:"id"`
	Action        string `json:"action"`
	SourceID      int64  `json:"source_id,omitempty"`
	DestinationID int64  `json:"destination_id,omitempty"`
	Label         string `json:"label,omitempty"`
	// Code is a stable identifier for Reason, so clients can explain a change without parsing its text.
	Code   string `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
}
type Report struct {
	Applicable   bool         `json:"applicable"`
	PlanToken    string       `json:"plan_token"`
	OperationID  string       `json:"operation_id,omitempty"`
	Summary      Summary      `json:"summary"`
	Changes      []Change     `json:"changes"`
	Conflicts    []Change     `json:"conflicts"`
	Warnings     []string     `json:"warnings"`
	AudioMatches []AudioMatch `json:"audio_matches"`
}
type row map[string]any
type entity struct {
	Kind        string
	Key         string
	ID          int64
	Values      row
	Performance row
	Art         row
	Members     []string
	Parent      string
	Next        string
	File        string
	Hash        string
	Size        int64
}
type baseline struct {
	Source      string
	Destination string
}
type state struct {
	Version   int
	Library   string
	Playlists map[string]string
	Peers     map[string]map[string]baseline
	Revisions map[string]uint64
	Tracks    map[string]string
}

// Registration replaces the Engine UUID a library synced under with its
// instance ID, so state recorded before registration still belongs to it.
func (st state) peer(c *collection) map[string]baseline {
	if b, ok := st.Peers[c.Instance]; ok {
		return b
	}
	return st.Peers[c.UUID]
}
func (st state) revision(c *collection) uint64 {
	if r, ok := st.Revisions[c.Instance]; ok {
		return r
	}
	return st.Revisions[c.UUID]
}

type collection struct {
	Path      string
	UUID      string
	Instance  string
	Registry  *libraryidentity.Registry
	Entities  map[string]*entity
	Playlists map[int64]string
	State     state
	Schema    map[string][]string
}
type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func digest(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func number(v any) int64                    { n, _ := v.(int64); return n }
func str(v any) string                      { s, _ := v.(string); return s }
func quoted(s string) string                { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func identity(uuid string, id int64) string { return uuid + ":" + strconv.FormatInt(id, 10) }
func statePath(path string) string          { return filepath.Join(path, ".engine-cli", "sync", "state.json") }
func loadState(path, uuid string) (state, error) {
	s := state{Version: 1, Library: uuid, Playlists: map[string]string{}, Peers: map[string]map[string]baseline{}, Revisions: map[string]uint64{}, Tracks: map[string]string{}}
	b, err := os.ReadFile(statePath(path))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if s.Version != 1 || s.Library != uuid || s.Playlists == nil || s.Peers == nil || s.Revisions == nil {
		return s, fmt.Errorf("sync state does not belong to this library or is damaged")
	}
	if s.Tracks == nil {
		s.Tracks = map[string]string{}
	}
	return s, nil
}
func openLibrary(path string, write bool) (*sql.DB, error) {
	p := filepath.Join(path, "Database2", "m.db")
	if info, err := os.Stat(p); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("library database is not a regular file")
	}
	mode := "ro"
	if write {
		mode = "rw"
	}
	uriPath := filepath.ToSlash(p)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=" + mode}
	return sql.Open("sqlite", u.String())
}
func rows(ctx context.Context, q queryer, table string) ([]row, []string, error) {
	r, err := q.QueryContext(ctx, "SELECT * FROM "+quoted(table))
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	cols, err := r.Columns()
	if err != nil {
		return nil, nil, err
	}
	result := []row{}
	for r.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err = r.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		v := row{}
		for i, c := range cols {
			v[c] = values[i]
		}
		result = append(result, v)
	}
	return result, cols, r.Err()
}
func clone(v row) row {
	r := row{}
	for k, x := range v {
		r[k] = x
	}
	return r
}
func without(v row, keys ...string) row {
	r := clone(v)
	for _, k := range keys {
		delete(r, k)
	}
	return r
}
func orderedKeys(m map[string]*entity) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func fileHash(ctx context.Context, path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("media is not a regular file: %s", path)
	}
	h := sha256.New()
	total, err := stream(ctx, h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), total, nil
}
func stream(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	const chunk = 128 * 1024
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := io.CopyN(dst, src, chunk)
		total += n
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}
func mediaPath(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, filepath.FromSlash(path))
}
