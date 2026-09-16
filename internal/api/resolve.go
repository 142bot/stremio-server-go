// Package api — play-time resolver: /{infoHash}/resolve
//
// GET /{ih}/resolve[?s=<season>&e=<episode>&name=<substring>]
//
// Pairs with URL-shaped add-on streams: waits for torrent metadata once, at
// playback time (never during search), selects the most likely playable file,
// and 302-redirects to the canonical enginefs stream /{ih}/{idx}, which then
// serves ranged bytes exactly as before. No existing endpoint changes shape.
//
// Selection ladder (first hit wins; fallbacks are log-observable):
//  1. name hint (case-insensitive substring) among video files
//  2. season/episode patterns among video files
//  3. largest video file (log: info)
//  4. engine's GuessFileIdx() heuristic (log: warn)
package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/M0Rf30/stremio-server-go/internal/logging"
	"github.com/M0Rf30/stremio-server-go/internal/types"
)

// resolveMetadataTimeout mirrors the 90 s metadata budget of the enginefs
// stream/create handlers.
const resolveMetadataTimeout = 90 * time.Second

func (s *server) handleResolve(w http.ResponseWriter, r *http.Request, ih string) {
	q := r.URL.Query()
	eng, err := s.em.EnsureEngine(ih, types.AddOptions{Trackers: q["tr"]})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ctx, cancel := withTimeout(r, resolveMetadataTimeout)
	defer cancel()
	if err := eng.Ready(ctx); err != nil {
		http.Error(w, "timed out waiting for metadata: "+err.Error(), http.StatusGatewayTimeout)
		return
	}
	files := eng.Files()
	season, _ := strconv.Atoi(q.Get("s"))
	episode, _ := strconv.Atoi(q.Get("e"))
	idx := pickStreamable(files, q.Get("name"), season, episode, eng)
	if idx < 0 {
		logging.For("resolve").Warn("no playable file", "ih", ih,
			"season", season, "episode", episode, "nameHint", q.Get("name"))
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/"+ih+"/"+strconv.Itoa(idx), http.StatusFound)
}

// requestBaseURL derives this server's externally reachable base from the
// incoming request, honoring reverse-proxy headers (Traefik supplies both).
// Precedence: X-Forwarded-Proto / X-Forwarded-Host, then request TLS + Host.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

// isVideoFile reports whether name ends in a known video container extension
// (the package-level videoMimes map doubles as the extension allowlist).
func isVideoFile(name string) bool {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return false
	}
	_, ok := videoMimes[strings.ToLower(name[i:])]
	return ok
}

// pickStreamable implements the selection ladder documented above. Returns -1
// only when no candidates exist anywhere.
func pickStreamable(files []types.FileInfo, nameHint string, season, episode int, eng types.Engine) int {
	videoIdx := make([]int, 0, len(files))
	for i, f := range files {
		if isVideoFile(f.Name) {
			videoIdx = append(videoIdx, i)
		}
	}
	if len(videoIdx) == 0 {
		logging.For("resolve").Warn("no video files; engine heuristic", "files", len(files))
		return eng.GuessFileIdx()
	}

	// 1. explicit name hint, case-insensitive substring
	if h := strings.ToLower(strings.TrimSpace(nameHint)); h != "" {
		for _, i := range videoIdx {
			if strings.Contains(strings.ToLower(files[i].Name), h) {
				return i
			}
		}
	}

	// 2. season/episode patterns, strongest first
	if season > 0 && episode > 0 {
		patterns := []string{
			fmt.Sprintf(`(?i)s%02de%02d`, season, episode),
			fmt.Sprintf(`(?i)\b%dx%02d\b`, season, episode),
			fmt.Sprintf(`(?i)s%d ?e%d`, season, episode),
		}
		for _, p := range patterns {
			re, err := regexp.Compile(p)
			if err != nil {
				continue
			}
			for _, i := range videoIdx {
				if re.MatchString(files[i].Name) {
					return i
				}
			}
		}
	}

	// 3. largest video file
	best, bestLen := -1, int64(-1)
	for _, i := range videoIdx {
		if files[i].Length > bestLen {
			best, bestLen = i, files[i].Length
		}
	}
	logging.For("resolve").Info("largest-video fallback", "file", files[best].Name)
	return best
}
