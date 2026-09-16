package api

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/M0Rf30/stremio-server-go/internal/types"
)

type fakeEngine struct {
	files    []types.FileInfo
	guess    int
	readyErr error
	ranReady bool
	ih       string
}

func (f *fakeEngine) InfoHash() string { return f.ih }
func (f *fakeEngine) Ready(context.Context) error {
	f.ranReady = true
	return f.readyErr
}
func (f *fakeEngine) Files() []types.FileInfo  { return f.files }
func (f *fakeEngine) Stats(int) *types.Stats   { return &types.Stats{InfoHash: f.ih} }
func (f *fakeEngine) NewReader(int) (io.ReadSeekCloser, int64, error) {
	return nil, 0, errors.New("not implemented")
}
func (f *fakeEngine) GuessFileIdx() int { return f.guess }

type fakeEngineManager struct{ eng *fakeEngine }

func (m *fakeEngineManager) EnsureEngine(string, types.AddOptions) (types.Engine, error) {
	return m.eng, nil
}
func (m *fakeEngineManager) GetEngine(string) (types.Engine, bool) { return m.eng, m.eng != nil }
func (m *fakeEngineManager) RemoveEngine(string) error             { return nil }
func (m *fakeEngineManager) RemoveAll()                            {}
func (m *fakeEngineManager) ListEngines() []string                 { return nil }
func (m *fakeEngineManager) AllStats() map[string]*types.Stats     { return nil }
func (m *fakeEngineManager) Close() error                          { return nil }

const testIH = "ad6bfbcd2bf30a22b86d312ec1da8567ab8ec036"

func resolveRequest(t *testing.T, srv *server, url string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", url, nil)
	srv.handleResolve(rec, req, testIH)
	return rec.Code, rec.Header().Get("Location")
}

func TestResolveSeasonEpisodeWins(t *testing.T) {
	e := &fakeEngine{ih: testIH, guess: 0, files: []types.FileInfo{
		{Name: "Show.S01E01.1080p.mkv", Length: 500},
		{Name: "Show.S01E02.1080p.mkv", Length: 400},
		{Name: "bonus-featurette.mp4", Length: 999},
	}}
	s := &server{em: &fakeEngineManager{eng: e}}
	code, loc := resolveRequest(t, s, "/"+testIH+"/resolve?s=1&e=2")
	if code != 302 || loc != "/"+testIH+"/1" {
		t.Fatalf("got %d %q; want 302 /%s/1", code, loc, testIH)
	}
}

func TestResolveNameHintWins(t *testing.T) {
	e := &fakeEngine{ih: testIH, guess: 0, files: []types.FileInfo{
		{Name: "Feature-2020.1080p.mp4", Length: 100},
		{Name: "Making-Of.mp4", Length: 300},
	}}
	s := &server{em: &fakeEngineManager{eng: e}}
	code, loc := resolveRequest(t, s, "/"+testIH+"/resolve?name=feature")
	if code != 302 || loc != "/"+testIH+"/0" {
		t.Fatalf("got %d %q; want 302 /%s/0", code, loc, testIH)
	}
}

func TestResolveLargestVideoFallback(t *testing.T) {
	e := &fakeEngine{ih: testIH, guess: 0, files: []types.FileInfo{
		{Name: "English.srt", Length: 4617},
		{Name: "Poster.jpg", Length: 23255},
		{Name: "Movie.1972.1080p.mp4", Length: 2934775277},
	}}
	s := &server{em: &fakeEngineManager{eng: e}}
	code, loc := resolveRequest(t, s, "/"+testIH+"/resolve")
	if code != 302 || loc != "/"+testIH+"/2" {
		t.Fatalf("got %d %q; want 302 /%s/2", code, loc, testIH)
	}
}

func TestResolveMetadataTimeout504(t *testing.T) {
	e := &fakeEngine{ih: testIH, readyErr: errors.New("ctx done")}
	s := &server{em: &fakeEngineManager{eng: e}}
	code, _ := resolveRequest(t, s, "/"+testIH+"/resolve")
	if code != 504 {
		t.Fatalf("got %d; want 504", code)
	}
}

func TestResolveNoPlayable404(t *testing.T) {
	e := &fakeEngine{ih: testIH, guess: -1}
	s := &server{em: &fakeEngineManager{eng: e}}
	code, _ := resolveRequest(t, s, "/"+testIH+"/resolve")
	if code != 404 {
		t.Fatalf("got %d; want 404", code)
	}
}
