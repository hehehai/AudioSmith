package translate

import "testing"

func kittenScript() StoryScript {
	return StoryScript{Title: "我想领养一只小猫", Cast: []StoryRole{{Name: "孩子", Voice: "明亮的年轻女声"}, {Name: "妈妈", Persona: "温柔的妈妈"}},
		Lines: []StoryScriptLine{
			{Speaker: "孩子", Text: "妈妈，我想领养一只小猫，可以吗？"},
			{Speaker: "妈妈", Text: "领养是好事，可是养猫要花很多时间照顾它。你能一直照顾它吗？"},
			{Speaker: "孩子", Text: "我能！我每天喂它，给它换水，铲猫砂。", Direction: "急切保证"},
			{Text: "妈妈笑了。"},
		}}
}

// TestClipScript: English is no longer cut at the Chinese limits
// ("skeptical, testing the e", thread #44); too-long text ends at a
// phrase or word break, Chinese at a clause break.
func TestClipScript(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"bursting with excitement", "bursting with excitement"},
		{"brightly, pleadingly, hopeful, almost breathless, words tumbling out over each other in a rush", "brightly, pleadingly, hopeful, almost breathless"},
		{"yielding warmly agreeing to visit the shelter this very Saturday morning together", "yielding warmly agreeing to visit the shelter this very"},
		{"温和但坚定，语气沉稳，慢慢地把每一个字都说得清清楚楚，带着一点无奈的笑", "温和但坚定，语气沉稳"},
	} {
		if got := clipScript(c.in, 24, 60); got != c.want {
			t.Errorf("%q: %q, want %q", c.in, got, c.want)
		}
	}
}
