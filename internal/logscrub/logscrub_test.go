package logscrub

import (
	"strings"
	"testing"
)

func TestLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{`toast text="Saved “Chapter 3 – The Lighthouse”"`, `toast text=…`},
		{`studio: render name=GrandmaVoice file=Interview.wav ms=1200`, `studio: render name=… file=… ms=1200`},
		{`longform: "The Lighthouse" is done: removed 2 earlier unfinished renders`, `longform: "…" is done: removed 2 earlier unfinished renders`},
		{`capture 7: Anna's AirPods Pro did not start with voice processing (err); recording without it`, `capture 7: … did not start with voice processing (err); recording without it`},
		{`updates: GET https://actnow.dev/updates/audiosmith/beta?x=1 503`, `updates: GET https://actnow.dev/updates/audiosmith/beta?… 503`},
		{`dub: Get "https://www.youtube.com/watch?v=abc": EOF`, `dub: Get "https://www.youtube.com/…": EOF`},
		{`export: could not write ~/Documents/Podcast with Anna.m4a: disk full`, `export: could not write ~/Documents/….m4a: disk full`},
		{`engine: loading ~/.cache/huggingface/hub/models--mlx-community--Kokoro-82M-bf16/snapshots/0123abcd4567/config.json`, `engine: loading ~/.cache/huggingface/hub/models--mlx-community--Kokoro-82M-bf16/snapshots/0123abcd4567/config.json`},
		{`data folder ~/Library/Application Support/AudioSmith/logs/audiosmith.log`, `data folder ~/Library/Application Support/AudioSmith/logs/audiosmith.log`},
	}
	for _, c := range cases {
		if got := Line(c.in); got != c.want {
			t.Errorf("Line(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestLineLeavesNoUserText(t *testing.T) {
	in := `toast text="Lighthouse chapter ready" name=Lighthouse voice=Grandma; ` +
		`capture 3: Lighthouse Mic stopped; started again (2); open https://docs.example.com/lighthouse/plan?id=7; ~/Movies/Lighthouse trailer.mov`
	out := Line(in)
	for _, w := range []string{"Lighthouse", "lighthouse", "Grandma", "trailer", "plan"} {
		if strings.Contains(out, w) {
			t.Errorf("%q still has %q", out, w)
		}
	}
	if !strings.Contains(out, "https://docs.example.com/…") || !strings.Contains(out, ".mov") {
		t.Errorf("the site or the extension is gone: %q", out)
	}
}
