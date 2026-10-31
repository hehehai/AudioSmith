package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// TestSnapshotsKitGallery renders every component of the 0.5 kit (the
// UI overhaul's P1, plan/ui-overhaul-plan.md §4) on one page, light and
// dark, in English and Chinese (kit-gallery-<look>.png), and the intro
// dialog and the corner tip over it (kit-intro-<look>.png). The pictures
// are for review, not compared:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/shots go test -run TestSnapshotsKitGallery ./app
func TestSnapshotsKitGallery(t *testing.T) { runSnapshots(t, snapshotsKitGallery) }

func snapshotsKitGallery(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, run := range []struct {
		lang string
		dark bool
	}{{"en", false}, {"en", true}, {"zh-Hans", false}, {"zh-Hans", true}} {
		i18n.SetLocale("en")
		a, _, _ := newTestApp(t)
		if run.lang != "en" {
			a.updateSettings(func(s *settings.Settings) { s.General.Language = run.lang })
		}
		zh := run.lang != "en"
		L := func(en, cn string) string {
			if zh {
				return cn
			}
			return en
		}
		g := &kitGallery{a: a, L: L, speed: 1, pos: 0.35, sw1: true, filter: L("English", "英语")}
		showIntro := false
		tt := ui.NewTester(func(c *ui.Context) {
			a.applyTheme(c)
			ui.Scroll(c.Key("kit")).Fill().Background(tok(c.Theme()).Bg).Children(func() { g.build(c) })
			if showIntro {
				a.introDialog(c, "intro:gallery", intro{
					Title: L("Introducing Dubbing", "视频配音上线了"), Icon: icons.VideoFramePlayHorizontal,
					Points: [3]introPoint{
						{icons.Global, L("Any language to any language", "任意语言互译"), L("Transcribe, translate and re-voice a video on this Mac.", "在本机转写、翻译并重新配音。")},
						{icons.UserSpeakRounded, L("The speakers keep their voices", "说话人保留原声"), L("Each speaker is cloned from the video itself.", "每位说话人都从视频里克隆。")},
						{icons.Pen, L("Every line stays editable", "每一句都能改"), L("Fix a translation or a timing, then dub that line again.", "改译文、改时间，再重配这一句。")},
					},
				})
			}
			if !showIntro {
				cornerTip(c, "kit-tip", icons.LightbulbMinimalistic, L("Tip: ⌘K finds any page", "提示：⌘K 可以搜索任何页面"),
					L("Search pages, voices and projects.", "页面、音色和项目都能搜。"), func() {})
			}
		}, 1100, 2100)
		tt.SetDark(run.dark)
		tt.SetScale(2)
		suffix := "light"
		if run.dark {
			suffix = "dark"
		}
		if zh {
			suffix += "-zh-Hans"
		}
		save := func(name string) {
			tt.Frame()
			tt.Frame()
			tt.Frame()
			f, err := os.Create(filepath.Join(out, name+"-"+suffix+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		save("kit-gallery")
		showIntro = true
		tt.SetSize(1100, 760)
		save("kit-intro")
	}
}

// kitGallery is the state of the gallery's controls.
type kitGallery struct {
	a                *app
	L                func(en, zh string) string
	tab, seg, cat    int
	speed, pos, gain float64
	sw1, sw2         bool
	filter, text     string
}

func (g *kitGallery) build(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	L := g.L
	section := func(title string, fn func()) {
		ui.Column(c.Key(title)).Gap(px(t, sp3)).Children(func() {
			ui.Text(c, title).FontSize(px(t, fsCaption)).TextColor(k.Text3).FontWeight(500)
			fn()
		})
	}
	row := func(fn func()) { ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(fn) }
	ui.Column(c).Padding(px(t, sp6), px(t, 40)).Gap(px(t, sp6)).Children(func() {
		ui.Text(c, L("Kit gallery", "组件一览")).FontSize(px(t, fsTitle)).FontWeight(fwPageTitle)
		section(L("Buttons", "按钮"), func() {
			row(func() {
				primaryButton(c.Key("p"), L("Generate", "生成"))
				primaryIconButton(c.Key("pi"), icons.Add, L("New project", "新建项目"))
				secondaryButton(c.Key("s"), L("Upload", "上传"))
				secondaryIconButton(c.Key("si"), icons.UploadMinimalistic, L("Import", "导入"))
				textButton(c.Key("tx"), L("Learn more", "了解更多"), false)
				textButton(c.Key("tv"), L("View all", "查看全部"), true)
				ghostButton(c.Key("g"), L("Ghost", "幽灵按钮"))
				iconButton(c.Key("ib"), icons.MenuDots, L("More actions", "更多操作"))
				outlineIconButton(c.Key("of"), icons.Filter, L("Filters", "筛选"), false)
				outlineIconButton(c.Key("ol"), icons.List, L("List", "列表"), true)
				submitButton(c.Key("sub0"), L("Send", "发送"), false)
				submitButton(c.Key("sub1"), L("Send", "发送"), true)
				recordButton(c.Key("rec"), L("Record", "录音"))
			})
			ui.Column(c).Width(px(t, 300)).Children(func() {
				primaryButtonWide(c.Key("pw"), icons.Icon{}, L("Get started", "开始使用"))
			})
		})
		section(L("Tabs and segments", "标签与分段"), func() {
			underlineTabs(c.Key("tabs"), &g.tab, L("Settings", "设置"), L("History", "历史"), L("Speakers", "说话人")).Label("tabs").AlignSelf(ui.Stretch)
			row(func() {
				segmentedFit(c.Key("seg"), &g.seg, L("Coding agent", "编程代理"), L("MCP", "MCP 配置"), "cURL").Label("seg")
			})
		})
		section(L("Chips", "胶囊"), func() {
			row(func() {
				for i, cat := range []struct {
					ic   icons.Icon
					name string
				}{{icons.Dialog2, L("Conversational", "对话")}, {icons.NotebookMinimalistic, L("Narration", "旁白")}, {icons.SmileCircle, L("Characters", "角色")}} {
					if categoryChip(c.Key(i), cat.ic, cat.name, g.cat == i).Clicked() {
						g.cat = i
					}
				}
			})
			row(func() {
				filterChip(c.Key("f0"), L("Gender", "性别"), "")
				if _, cl := filterChip(c.Key("f1"), L("Language", "语言"), g.filter); cl {
					g.filter = ""
				}
				filterChip(c.Key("f2"), L("Created by", "创建者"), L("Me", "我"))
			})
			row(func() {
				exampleChip(c.Key("e0"), icons.NotebookMinimalistic, L("Narrate a story", "讲一个故事"))
				exampleChip(c.Key("e1"), icons.SmileCircle, L("Tell a silly joke", "讲个笑话"))
				exampleChip(c.Key("e2"), icons.Microphone3, L("Record an ad", "录一段广告"))
				chip(c.Key("c0"), L("Calm", "平静"), "", true)
				chip(c.Key("c1"), L("Warm", "温暖"), "", false)
				chip(c.Key("c2"), "S1", "12", false)
			})
		})
		section(L("Form controls", "表单"), func() {
			ui.Row(c).Gap(px(t, sp6)).AlignItems(ui.Start).Children(func() {
				ui.Column(c).Width(px(t, 340)).Gap(px(t, sp3)).Children(func() {
					selectorRow(c.Key("sel-voice"), icons.Icon{}, "Serena", "Serena", L("Warm young female · Chinese", "温柔的青年女声 · 中文"), L("Voice", "音色"))
					selectorRow(c.Key("sel-model"), icons.Box, "", "Qwen3-TTS 1.7B", L("Clone and design · 4.3 GB", "克隆与设计 · 4.3 GB"), L("Model", "模型"))
					sliderField(c, "speed", L("Speed", "语速"), "1.00×", &g.speed, 0.5, 2, 0.05, L("Slower", "更慢"), L("Faster", "更快"))
				})
				ui.Column(c).Width(px(t, 300)).Gap(px(t, sp3)).Children(func() {
					slimSlider(c.Key("gain"), &g.gain, 0, 1).Label("gain")
					seekSlider(c.Key("seek"), &g.pos, 0, 1).Label("seek")
					ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
						inkSwitch(c.Key("sw1"), &g.sw1).Label("on")
						inkSwitch(c.Key("sw2"), &g.sw2).Label("off")
						ui.Checkbox(c.Key("cb"), &g.sw1, L("Selected", "已选"))
					})
					ui.TextInput(c.Key("in"), &g.text).Placeholder(L("A text field", "文本框"))
				})
			})
		})
		section(L("Cards", "卡片"), func() {
			ui.Row(c).Gap(px(t, sp4)).AlignItems(ui.Start).Children(func() {
				ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
					composerCard(c, "composer", func() {
						dropZone(c, icons.UploadMinimalistic, L("Click to upload, or drag and drop", "点击上传，或拖入文件"), L("Audio or video, up to 2 hours", "音频或视频，最长 2 小时"), func() {
							secondaryIconButton(c.Key("rec"), icons.Microphone3, L("Record audio", "录音"))
						})
					}, func() {
						chip(c.Key("m"), "Qwen3-ASR", "", false)
						chip(c.Key("l"), L("Auto-detect language", "自动识别语言"), "", false)
					}, func() { caption(c, L("2 files · 3:41", "2 个文件 · 3:41")) }, func() {
						submitButton(c.Key("go"), L("Transcribe", "转写"), true)
					})
				})
				ui.Column(c).Width(px(t, 330)).Gap(px(t, sp3)).Children(func() {
					promoCard(c.Key("promo"), icons.Dialog2, L("Try Stories", "试试「故事」"), L("Cast voices and write a scene with dialogue.", "给角色配音色，写一场有对白的戏。"), func() {
						secondaryButton(c.Key("try"), L("Try now", "试一下"))
					}, func() {})
					ui.Row(c).Gap(px(t, sp1)).Justify(ui.SpaceBetween).Children(func() {
						for i, tl := range []struct {
							ic   icons.Icon
							name string
						}{{icons.Soundwave, L("Speech", "语音")}, {icons.UserPlusRounded, L("Clone", "克隆")}, {icons.MagicStick3, L("Design", "设计")},
							{icons.VideoFramePlayHorizontal, L("Dub", "配音")}, {icons.Subtitles, L("Transcribe", "转写")}} {
							toolTile(c.Key(i), tl.ic, tl.name)
						}
					})
				})
			})
			ui.Row(c).Gap(px(t, sp3)).Children(func() {
				for i, ic := range []struct {
					tint      string
					title, by string
				}{{"#6b5b95", L("Two-voice short story", "两人对话短故事"), "SoulX"}, {"#3d7f73", L("Audiobook chapter", "有声书一章"), L("Narrator and two roles", "旁白加两个角色")},
					{"#9a6b3f", L("Product narration", "产品旁白"), L("Chinese and English", "中英各一段")}, {"#4a5d8a", L("Dubbing sample", "配音示例"), L("English → Chinese", "英文 → 中文")}} {
					imageCard(c.Key(i), nil, ui.Hex(ic.tint), ic.title, ic.by).Grow(1).Basis(0).MinWidth(0)
				}
			})
			ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Stretch).Children(func() {
				featureCard(c.Key("fc1"), L("Instant clone", "快速克隆"), L("Clone a voice from 10 seconds of audio.", "用 10 秒音频克隆一个音色。"), func() {
					ui.Icon(c, icons.UserPlusRounded.Linear).Size(px(t, 40), px(t, 40)).TextColor(k.Text2)
				}, func() { primaryButtonWide(c.Key("go"), icons.Icon{}, L("Start", "开始")) }).Grow(1).Basis(0)
				featureCard(c.Key("fc2"), L("Voice design", "设计音色"), L("Describe a voice and hear it.", "描述一个声音，然后听听看。"), func() {
					ui.Icon(c, icons.MagicStick3.Linear).Size(px(t, 40), px(t, 40)).TextColor(k.Text2)
				}, func() {
					lift(c, secondaryButton(c.Key("go"), L("Design", "设计")).Height(px(t, 40)).AlignSelf(ui.Stretch))
				}).Grow(1).Basis(0)
				ui.Column(c).Grow(1).Basis(0).Gap(px(t, sp2)).Children(func() {
					suggestionCard(c.Key("s1"), L("A podcast intro", "播客开场"), L("Two hosts greet the listeners and introduce today's guest.", "两位主持人问候听众，介绍今天的嘉宾。"))
					suggestionCard(c.Key("s2"), L("A bedtime story", "睡前故事"), L("A calm narrator reads a short fable.", "平静的旁白读一则短寓言。"))
				})
			})
		})
		section(L("Table rows", "表格行"), func() {
			ui.Column(c).Children(func() {
				for i, r := range []struct{ name, desc, lang, dur string }{
					{"interview.m4a", L("Two speakers · transcribed", "两位说话人 · 已转写"), L("English", "英语"), "1:14"},
					{"keynote.mov", L("Dubbed into Chinese", "已配成中文"), L("Chinese", "中文"), "12:03"},
					{"ad-spot.wav", L("Narration", "旁白"), L("English", "英语"), "0:30"},
				} {
					tableRow(c.Key(i), r.name, i == 1, func() {
						ui.Avatar(c, r.name, nil)
						ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 1)).Children(func() {
							ui.Text(c, r.name).SingleLine()
							caption(c, r.desc).SingleLine()
						})
						ui.Text(c, r.lang).FontSize(px(t, fsCaption)).TextColor(k.Text2).Width(px(t, 80)).Shrink(0)
						ui.Text(c, r.dur).FontSize(px(t, fsCaption)).TextColor(k.Text2).Width(px(t, 50)).Shrink(0)
					}, func() {
						playToggle(c.Key("play"), false, L("Play", "播放"), L("Pause", "暂停"))
						iconButton(c.Key("dl"), icons.DownloadMinimalistic, L("Download", "下载"))
						moreMenu(c.Key("more"), "", func(*ui.Menu) {})
					})
				}
			})
		})
		section(L("Empty state: ghost rows", "空状态：幽灵行"), func() {
			ghostRows(c, 4, icons.Subtitles, L("No transcripts yet", "还没有转写"), L("Drop a recording above to transcribe it.", "把录音拖到上方即可转写。"), nil)
		})
	})
}
