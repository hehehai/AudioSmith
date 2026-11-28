package dub

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestInfoFixtures reads what real yt-dlp 2026.08.19 said about videos of
// several sites (recorded on the Mac through the proxy, metadata only,
// with Download's format; formats, automatic captions and stream
// addresses cut out): testdata/ytdlp-info/<site>.json.
func TestInfoFixtures(t *testing.T) {
	for _, tc := range []struct {
		file string
		want SourceMeta
		// thumb is a part of the cover's address chosen.
		thumb string
	}{
		{"youtube", SourceMeta{Site: "Youtube", SiteName: "YouTube", ID: "jNQXAC9IVRw", Title: "Me at the zoo", Channel: "jawed",
			Uploaded: "2005-04-24", Duration: 19, Views: 446162447, Captions: []string{"de", "en"}, Format: "133+140", Ext: "mp4",
			VCodec: "avc1.4D400C", ACodec: "mp4a.40.2", Width: 320, Height: 240, FPS: 15, Size: 433081 + 309288}, "i.ytimg.com/vi"},
		{"bilibili", SourceMeta{Site: "BiliBili", SiteName: "Bilibili", Title: "【官方 MV】Never Gonna Give You Up - Rick Astley",
			Channel: "索尼音乐中国", Uploaded: "2019-12-31", Views: 107601128, Format: "30080+30280", Ext: "mp4", VCodec: "avc1.640032",
			ACodec: "mp4a.40.2", Width: 1920, Height: 1080, FPS: 25, Size: 69809437 + 5410339, Approx: true}, "hdslb.com"},
		{"vimeo", SourceMeta{Site: "Vimeo", SiteName: "Vimeo", ID: "76979871", Title: "The New Vimeo Player (You Know, For Videos)",
			Channel: "Vimeo", Duration: 62, Views: -1, Language: "en", Captions: []string{"de", "en", "es", "fr"}, Ext: "mp4",
			VCodec: "avc1.64001F", Width: 1280, Height: 720, FPS: 23.976}, "vimeocdn.com"},
		{"tiktok", SourceMeta{Site: "TikTok", SiteName: "TikTok", ID: "6718335390845095173", Channel: "Scout, Suki & Stella",
			Uploaded: "2019-07-27", Duration: 10, Views: 159600, Ext: "mp4", VCodec: "h265", ACodec: "aac", Width: 576, Height: 1024,
			Size: 1728265}, "tiktokcdn.com"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "ytdlp-info", tc.file+".json"))
			if err != nil {
				t.Fatal(err)
			}
			info, err := parseInfo(data)
			if err != nil {
				t.Fatal(err)
			}
			m := info.Meta
			if !strings.Contains(m.Thumb, tc.thumb) {
				t.Errorf("cover %q, want one of %s", m.Thumb, tc.thumb)
			}
			if (tc.file == "youtube" || tc.file == "tiktok") && m.Description == "" {
				t.Error("no description")
			}
			if tc.file == "bilibili" && m.Description != "" {
				t.Errorf("Bilibili's %q is no description", m.Description)
			}
			w := tc.want
			if w.ID == "" {
				w.ID = m.ID
			}
			if w.Title == "" {
				w.Title = m.Title // TikTok's: emoji and hashtags
			}
			if w.Duration == 0 {
				w.Duration = m.Duration
			}
			if w.Format == "" {
				w.Format = m.Format
			}
			m.Page, m.Description, m.Thumb = "", "", ""
			if !slices.Equal(m.Captions, w.Captions) {
				t.Errorf("captions %q, want %q", m.Captions, w.Captions)
			}
			m.Captions, w.Captions = nil, nil
			if !reflect.DeepEqual(m, w) {
				t.Errorf("got  %+v\nwant %+v", m, w)
			}
			if !m.HasVideo() {
				t.Error("no picture")
			}
		})
	}
}

// TestPickThumb chooses covers.
func TestPickThumb(t *testing.T) {
	ts := []rawThumb{
		{URL: "https://i.ytimg.com/vi/x/default.jpg", Width: 120},
		{URL: "https://i.ytimg.com/vi_webp/x/hqdefault.webp", Width: 480},
		{URL: "https://i.ytimg.com/vi/x/hqdefault.jpg", Width: 480},
		{URL: "https://i.ytimg.com/vi/x/maxres.avif", Width: 1280},
		{URL: "https://i.ytimg.com/vi/x/huge.jpg", Width: 3840},
		{URL: "file:///etc/passwd", Width: 640},
	}
	if got := pickThumb(ts, ""); got != "https://i.ytimg.com/vi/x/hqdefault.jpg" {
		t.Errorf("picked %q", got)
	}
	if got := pickThumb(ts[4:5], ""); got != "https://i.ytimg.com/vi/x/huge.jpg" {
		t.Errorf("only a big one: %q", got)
	}
	if got := pickThumb([]rawThumb{{URL: "https://a.example/1.jpg"}, {URL: "https://a.example/2.png"}}, ""); got != "https://a.example/2.png" {
		t.Errorf("no widths: yt-dlp's last, got %q", got)
	}
	if got := pickThumb(nil, "javascript:alert(1)"); got != "" {
		t.Errorf("not http(s): %q", got)
	}
	if got := pickThumb(nil, "http://i0.hdslb.com/a.jpg"); got != "http://i0.hdslb.com/a.jpg" {
		t.Errorf("yt-dlp's one: %q", got)
	}
}

// TestSiteName names sites, with the domain for others.
func TestSiteName(t *testing.T) {
	for _, tc := range [][4]string{
		{"Youtube", "", "", "YouTube"}, {"BiliBili", "", "", "Bilibili"}, {"BilibiliBangumi", "", "", "Bilibili"},
		{"Twitter", "", "", "X"}, {"Douyin", "", "", "抖音"}, {"Generic", "", "https://www.example.org/v/1", "example.org"},
		{"Generic", "", "", ""}, {"Odysee", "odysee.com", "", "odysee.com"}, {"Odysee", "", "", "Odysee"},
	} {
		if got := siteName(tc[0], tc[1], tc[2]); got != tc[3] {
			t.Errorf("siteName(%q, %q, %q) = %q, want %q", tc[0], tc[1], tc[2], got, tc[3])
		}
	}
}

// TestInfoErrors: yt-dlp's last error line (recorded on the Mac) comes
// back as the detail, without "ERROR: [site] id:".
func TestInfoErrors(t *testing.T) {
	bin := scriptYtDlp(t, `cat "$AS_ERR" >&2; exit 1`, "@echo off\r\ntype \"%AS_ERR%\" 1>&2\r\nexit /b 1\r\n")
	for name, want := range map[string]string{
		"vimeo": "The web client only works when logged-in", "twitter": "No video could be found in this tweet",
		"douyin": "Fresh cookies", "private": "This video is unavailable", "unsupported": "Unsupported URL: https://example.com/",
	} {
		t.Setenv("AS_ERR", filepath.Join(mustAbs(t, "testdata/ytdlp-info"), name+".err"))
		_, err := YtDlp{Path: bin}.Info(context.Background(), "https://example.com/v")
		var ue *URLError
		if !errors.As(err, &ue) || ue.Code != CodeYtDlp || !strings.HasPrefix(ue.Detail, want) && !strings.Contains(ue.Detail, want) {
			t.Errorf("%s: %v", name, err)
		}
		if ue != nil && strings.HasPrefix(ue.Detail, "ERROR") {
			t.Errorf("%s: detail %q keeps the prefix", name, ue.Detail)
		}
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// scriptYtDlp writes a shell/batch script standing for yt-dlp.
func scriptYtDlp(t *testing.T, shBody, winBody string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		bin := filepath.Join(t.TempDir(), "yt-dlp.cmd")
		if err := os.WriteFile(bin, []byte(winBody), 0o755); err != nil {
			t.Fatal(err)
		}
		return bin
	}
	bin := filepath.Join(t.TempDir(), "yt-dlp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+shBody+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestFetchThumbnail keeps JPEG, PNG and WebP covers within the limits,
// and refuses the rest, local addresses (also after a redirect) and
// other schemes.
func TestFetchThumbnail(t *testing.T) {
	var jpg bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 64, 36))
	img.Set(1, 1, color.RGBA{200, 10, 10, 255})
	jpeg.Encode(&jpg, img, nil)
	var gf bytes.Buffer
	gif.Encode(&gf, img, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/a.jpg", func(w http.ResponseWriter, r *http.Request) { w.Write(jpg.Bytes()) })
	mux.HandleFunc("/a.gif", func(w http.ResponseWriter, r *http.Request) { w.Write(gf.Bytes()) })
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) })
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) { w.Write(make([]byte, ThumbMaxBytes+10)) })
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:3901/secret.jpg", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	dir := t.TempDir()
	local := ThumbOptions{allowPrivate: true}
	name, err := FetchThumbnail(context.Background(), srv.URL+"/a.jpg", dir, local)
	if err != nil || name != "thumb.jpg" {
		t.Fatalf("jpeg: %q %v", name, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "thumb.jpg")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/a.gif", "/page", "/big", "/missing"} {
		if name, err := FetchThumbnail(context.Background(), srv.URL+path, dir, local); err == nil {
			t.Errorf("%s: kept as %q", path, name)
		}
	}
	// The checks themselves: the test server is on this machine.
	public := ThumbOptions{Lookup: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.215.14")}, nil
	}}
	for _, u := range []string{srv.URL + "/a.jpg", "file:///etc/passwd", "http://localhost/a.jpg", "http://10.0.0.1/a.jpg", "http://[::1]/a.jpg"} {
		if _, err := FetchThumbnail(context.Background(), u, dir, public); err == nil {
			t.Errorf("%s fetched", u)
		}
	}
	// Through a "proxy" (the test server itself, as a plain HTTP proxy
	// takes absolute URLs), a redirect to this machine is refused.
	if _, err := FetchThumbnail(context.Background(), "http://cover.example.com/redirect", dir, ThumbOptions{Lookup: public.Lookup, Proxy: srv.URL}); err == nil {
		t.Error("followed a redirect to 127.0.0.1")
	} else if !errors.Is(err, ErrURLRefused) {
		t.Errorf("redirect: %v, want a refusal", err)
	}
	if _, err := FetchThumbnail(context.Background(), "http://cover.example.com/a.jpg", dir, ThumbOptions{Lookup: public.Lookup, Proxy: srv.URL}); err != nil {
		t.Errorf("through the proxy: %v", err)
	}
}

// Without ffmpeg the probe still reads a site that has only separate
// streams (YouTube), and says the download needs ffmpeg.
func TestInfoWithoutFFmpeg(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	bin := scriptYtDlp(t,
		`printf '%s\n' "$@" > "`+args+`"; echo '{"id":"x","title":"T","format_id":"137+140","requested_formats":[{"vcodec":"avc1"},{"acodec":"mp4a"}]}'`,
		"@echo off\r\n"+
			"if exist \""+args+"\" del /q \""+args+"\"\r\n"+
			":loop\r\n"+
			"if \"%~1\"==\"\" goto done\r\n"+
			"echo \"%~1\">>\""+args+"\"\r\n"+
			"shift\r\n"+
			"goto loop\r\n"+
			":done\r\n"+
			"echo {\"id\":\"x\",\"title\":\"T\",\"format_id\":\"137+140\",\"requested_formats\":[{\"vcodec\":\"avc1\"},{\"acodec\":\"mp4a\"}]}\r\n",
	)
	for _, ff := range []string{"", "/opt/ffmpeg"} {
		info, err := YtDlp{Path: bin, FFmpeg: ff}.Info(context.Background(), "https://example.com/v")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(args)
		fallback := strings.Contains(string(b), "/bv*+ba/bv*/ba")
		if info.Meta.NeedsFFmpeg != (ff == "") || fallback != (ff == "") {
			t.Errorf("ffmpeg %q: needs %v, fallback %v", ff, info.Meta.NeedsFFmpeg, fallback)
		}
	}
}
