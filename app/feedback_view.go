package main

// Settings → Feedback: the form, the submissions waiting, the history,
// the web form and the GitHub issue; the dialogs of a thread and of the
// diagnostics' preview (state: feedback_page.go). After Dicta's
// feedbackui.

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/actnowkit/feedback"

	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// feedbackSection builds Settings → Feedback.
func (a *app) feedbackSection(c *ui.Context) {
	if a.fbMgr == nil {
		settingGroup(c, i18n.T("Other ways"), func() { a.fbOtherWays(c) })
		return
	}
	if !a.fbRefreshed {
		a.fbRefresh()
	}
	a.fbFormView(c)
	a.fbPendingView(c)
	a.fbHistoryView(c)
	settingGroup(c, i18n.T("Other ways"), func() { a.fbOtherWays(c) })
	a.fbThreadDialog(c)
	a.fbPreviewDialog(c)
}

// fbOtherWays: the site's form, and an issue on GitHub (public).
func (a *app) fbOtherWays(c *ui.Context) {
	settingRow(c, "fb-web", i18n.T("Send feedback on the web"), "actnow.dev", func() {
		if ui.Button(c.Key("fb-web-open"), i18n.T("Open")).Clicked() {
			openExternal(a.fbWebURL())
		}
	})
	settingRow(c, "fb-github", i18n.T("Or open an issue on GitHub"), i18n.T("Public: anyone can read it. Settings → Advanced → Report a bug fills in the diagnostics."), func() {
		if ui.Button(c.Key("fb-github-open"), i18n.T("Open GitHub")).Clicked() {
			openExternal(issuesURL)
		}
	})
}

// problem is the form's text for the first of ps that is one of these.
func (a *app) fbProblem(ps ...feedback.Problem) string {
	if !a.fbTried {
		return ""
	}
	for _, x := range a.fbProblems {
		if slices.Contains(ps, x) {
			return feedbackProblemText(x)
		}
	}
	return ""
}

func fbProblemLine(c *ui.Context, key, text string) {
	if text == "" {
		return
	}
	t := c.Theme()
	caption(c.Key(key), text).TextColor(tok(t).Danger)
}

func (a *app) fbFormView(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	busy := a.fbBusy()
	settingGroup(c, i18n.T("New feedback"), func() {
		settingRow(c, "fb-kind", i18n.T("Type"), "", func() {
			labels := make([]string, len(feedback.Kinds))
			for i, kd := range feedback.Kinds {
				labels[i] = feedbackKindText(kd)
			}
			ui.Segmented(c.Key("fb-kind-seg"), &a.fbForm.kind, labels...).Label(i18n.T("Type")).Disabled(busy)
		})
		settingBlock(c, "fb-body", func() {
			ui.Text(c, i18n.X("feedback", "Description"))
			ui.TextArea(c.Key("fb-body-in"), &a.fbForm.body).Lines(5, 14).Placeholder(i18n.T("What happened, and what did you expect? Steps to reproduce help.")).
				Label(i18n.X("feedback", "Description")).Disabled(busy)
			fbProblemLine(c, "fb-body-problem", a.fbProblem(feedback.ProblemBodyShort, feedback.ProblemBodyLong, feedback.ProblemKind))
		})
		settingRow(c, "fb-email", i18n.T("Email"), "", func() {
			ui.TextInput(c.Key("fb-email-in"), &a.fbForm.email).Placeholder(i18n.T("So we can reply")).Label(i18n.T("Email")).Width(settingW(t)).Shrink(1).MinWidth(px(t, 120)).Disabled(busy)
		})
		if pr := a.fbProblem(feedback.ProblemEmailRequired, feedback.ProblemEmailInvalid); pr != "" {
			settingFoot(c, "fb-email-foot", func() { fbProblemLine(c, "fb-email-problem", pr) })
		}
		settingRow(c, "fb-files", i18n.T("Attachments"), i18n.T("Up to 3 images (made smaller to 5 MB each) and one video up to 20 MB (larger ones are compressed). Uploaded only when you send."), func() {
			if ui.Button(c.Key("fb-add-images"), i18n.T("Add screenshots")).Disabled(busy || len(a.fbForm.images) >= feedback.MaxImages).Clicked() {
				a.fbAddImages()
			}
			if ui.Button(c.Key("fb-add-video"), i18n.T("Add a screen recording")).Disabled(busy || a.fbForm.video != "").Clicked() {
				a.fbAddVideo()
			}
		})
		if len(a.fbForm.images) > 0 || a.fbForm.video != "" {
			settingFoot(c, "fb-files-list", func() {
				files := append(append([]string(nil), a.fbForm.images...), a.fbForm.video)
				for i, f := range files {
					if f == "" {
						continue
					}
					ui.Row(c.Key(fmt.Sprintf("fb-file-%d", i))).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
						ic := icons.Gallery
						if i == len(files)-1 && f == a.fbForm.video {
							ic = icons.Videocamera
						}
						ui.Icon(c, ic.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text2).Shrink(0)
						truncText(c, filepath.Base(f)).FontSize(px(t, fsCaption)).Grow(1).MinWidth(0)
						if iconButton(c.Key("remove"), icons.Close, i18n.T("Remove %s", filepath.Base(f))).Disabled(busy).Clicked() {
							if i < len(a.fbForm.images) {
								a.fbForm.images = slices.Delete(a.fbForm.images, i, i+1)
							} else {
								a.fbForm.video = ""
							}
						}
					})
				}
			})
		}
		fbProblemLine(c, "fb-files-problem", a.fbProblem(feedback.ProblemTooManyImages, feedback.ProblemImageMissing, feedback.ProblemImageTooLarge,
			feedback.ProblemImageType, feedback.ProblemVideoMissing, feedback.ProblemVideoTooLarge, feedback.ProblemVideoType))
		settingRow(c, "fb-diag", i18n.T("Include diagnostics"),
			i18n.T("This Mac's model and macOS version, AudioSmith's version and settings, and its recent log lines and last crash, with keys, passwords, names, email and IP addresses removed. Never your text, voices or files."),
			func() {
				ui.Switch(c.Key("fb-diag-switch"), &a.fbForm.diagnostics).Label(i18n.T("Include diagnostics")).Disabled(busy)
			})
		settingFoot(c, "fb-diag-foot", func() {
			ui.Row(c).Children(func() {
				if linkButton(c.Key("fb-preview"), i18n.T("See what is sent")).Clicked() {
					a.fbOpenPreview()
				}
			})
		})
		settingBlock(c, "fb-send", func() {
			a.fbStatus(c)
			ui.Row(c).Wrap().Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Grow(1).Basis(px(t, 200)).MinWidth(0).Wrap().GapX(px(t, 4)).AlignItems(ui.Center).Children(func() {
					caption(c, i18n.T("Your feedback goes to actnow.dev only when you send it, and is used only to answer you and improve AudioSmith."))
					ui.Link(c, i18n.T("Privacy policy"), feedbackPrivacyURL).FontSize(px(t, fsCaption))
				})
				if primaryButton(c.Key("fb-send-btn"), i18n.X("feedback", "Send")).Disabled(busy).Clicked() {
					a.fbSend()
				}
			})
		})
	})
}

// status is the line of the submission being sent, or just sent or
// failed.
func (a *app) fbStatus(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	st := a.fbSt
	switch st.Phase {
	case feedback.Preparing:
		ui.Row(c.Key("fb-status")).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(px(t, 14), px(t, 14))
			caption(c, i18n.T("Preparing attachments…"))
		})
	case feedback.Sending:
		size := ""
		if st.TotalBytes > 0 {
			size = humanBytes(st.SentBytes) + " / " + humanBytes(st.TotalBytes)
		}
		ui.Column(c.Key("fb-status")).Gap(px(t, 4)).Children(func() {
			caption(c, strings.TrimSpace(i18n.T("Sending… %s", size)))
			ui.Progress(c, st.Fraction()).Height(px(t, 3)).Label(i18n.T("Sending"))
		})
	case feedback.Sent:
		num := ""
		if st.Current != nil {
			num = st.Current.Number
		}
		notice(c, "fb-status", noticeOK, i18n.T("Thanks! Your feedback %s was sent. We'll reply by email and here.", num), "", nil, func() { a.fbMgr.Reset() })
		_ = k
	case feedback.Failed:
		notice(c, "fb-status", noticeError, feedbackFailText(st.Current, st.Err), fbErrText(st.Err), func() {
			if st.Current != nil && st.Step != "prepare" {
				id := st.Current.LocalID
				if ui.Button(c.Key("fb-retry"), i18n.T("Try again")).Clicked() {
					a.fbRetry(id)
				}
				if ui.Button(c.Key("fb-discard"), i18n.X("feedback", "Discard")).Clicked() {
					a.fbDiscard(id)
				}
			}
		}, func() { a.fbMgr.Reset() })
	}
}

func fbErrText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// pending lists the submissions of the outbox not being sent now.
func (a *app) fbPendingView(c *ui.Context) {
	var list []feedback.Submission
	for _, s := range a.fbSt.Pending {
		if a.fbBusy() && a.fbSt.Current != nil && a.fbSt.Current.LocalID == s.LocalID {
			continue
		}
		list = append(list, s)
	}
	if len(list) == 0 {
		return
	}
	busy := a.fbBusy()
	settingGroup(c, i18n.T("Waiting to send"), func() {
		for _, s := range list {
			note := timeAgo(s.CreatedAt)
			if s.LastError != "" {
				note += " · " + feedbackFailText(&s, nil)
			}
			settingRowNote(c, "fb-pending-"+s.LocalID, feedbackKindText(s.Kind)+": "+feedback.Summary(s.Body, 60), note, func() {
				id := s.LocalID
				if ui.Button(c.Key("fb-pending-retry-"+id), i18n.T("Try again")).Disabled(busy).Clicked() {
					a.fbRetry(id)
				}
				if ui.Button(c.Key("fb-pending-discard-"+id), i18n.X("feedback", "Discard")).Disabled(busy).Clicked() {
					a.fbDiscard(id)
				}
			})
		}
	})
}

// history lists this Mac's feedback, a dot on those with replies not read.
func (a *app) fbHistoryView(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	ui.Column(c).Gap(px(t, 6)).Shrink(0).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(px(t, sp2)).Margin(0, 0, 0, px(t, sp1)).Children(func() {
			sectionTitleText(c, i18n.T("Your feedback")).Grow(1)
			if a.fbRefreshing {
				ui.Spinner(c).Size(px(t, 12), px(t, 12))
			} else if iconButton(c.Key("fb-refresh"), icons.Refresh, i18n.T("Refresh")).Clicked() {
				a.fbRefresh()
			}
		})
		settingGroup(c, "", func() {
			if a.fbHistErr != "" {
				settingBlock(c, "fb-hist-err", func() {
					caption(c, i18n.T("Could not reach actnow.dev: %s", a.fbHistErr)).TextColor(k.Warning).Selectable()
				})
			}
			if len(a.fbHistory) == 0 {
				settingBlock(c, "fb-hist-none", func() {
					caption(c, i18n.T("Feedback you send from this Mac appears here."))
				})
				return
			}
			for _, e := range a.fbHistory {
				a.fbHistoryRow(c, e)
			}
		})
	})
}

func (a *app) fbHistoryRow(c *ui.Context, e feedback.HistoryEntry) {
	t := c.Theme()
	k := tok(t)
	settingBlock(c, "fb-hist-"+e.ID, func() {
		b := ui.ButtonBase(c.Key("fb-hist-open-"+e.ID)).Justify(ui.Start).AlignItems(ui.Center).Gap(px(t, sp2)).Padding(px(t, 2), 0).
			Label(e.Number + " " + e.Summary)
		b.Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
				ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
					if e.Unread() {
						statusDot(c, k.Accent).Tooltip(i18n.T("New reply"))
					}
					truncText(c, e.Summary).Grow(1)
				})
				note := strings.Join(nonEmpty(e.Number, feedbackStatusText(e.Status), feedbackKindText(e.Kind), timeAgo(e.CreatedAt)), " · ")
				if e.Unread() {
					note = i18n.T("New reply") + " · " + note
				}
				caption(c, note)
			})
			ui.Icon(c, icons.AltArrowRight.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text3).Shrink(0)
		})
		if b.Clicked() {
			a.fbOpenThread(e.ID)
		}
	})
}

func nonEmpty(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

// threadDialog is a feedback with its messages and the reply field.
func (a *app) fbThreadDialog(c *ui.Context) {
	if !a.fbThreadOpen {
		return
	}
	t := c.Theme()
	k := tok(t)
	title, sub, web := i18n.T("Your feedback"), "", ""
	for _, e := range a.fbHistory {
		if e.ID == a.fbThreadID {
			title, sub, web = e.Summary, strings.Join(nonEmpty(e.Number, feedbackStatusText(e.Status)), " · "), e.WebURL
		}
	}
	if a.fbThread != nil && a.fbThread.Item.WebURL != "" {
		web = a.fbThread.Item.WebURL
	}
	dialogFrame(c, "fb-thread", &a.fbThreadOpen, title, sub, px(t, 560), func() {
		switch {
		case a.fbLoading:
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(px(t, 14), px(t, 14))
				caption(c, i18n.T("Loading"))
			})
		case a.fbThreadErr != "":
			caption(c, i18n.T("Could not reach actnow.dev: %s", a.fbThreadErr)).TextColor(k.Warning).Selectable()
		case a.fbThread != nil:
			th := a.fbThread
			fbMessage(c, "fb-msg-body", i18n.T("You"), th.Item.CreatedAt.Local().Format("2006-01-02 15:04"), th.Body, false)
			for _, m := range th.Messages {
				who := i18n.T("You")
				if m.Author == "support" {
					who = i18n.X("feedback author", "Support")
				}
				fbMessage(c, "fb-msg-"+m.ID, who, m.CreatedAt.Local().Format("2006-01-02 15:04"), m.Body, m.Author == "support")
			}
			if len(th.Attachments) > 0 {
				names := make([]string, len(th.Attachments))
				for i, a := range th.Attachments {
					names[i] = a.Name
				}
				caption(c, i18n.T("Attachments")+": "+strings.Join(names, ", "))
			}
			ui.TextArea(c.Key("fb-reply-in"), &a.fbReplyText).Lines(3, 8).Placeholder(i18n.T("Your reply")).Label(i18n.T("Your reply")).Disabled(a.fbReplying)
		}
	}, func() {
		if web != "" {
			if linkButton(c.Key("fb-thread-web"), i18n.T("View on actnow.dev")).Clicked() {
				openExternal(web)
			}
		}
		ui.Box(c).Grow(1)
		if a.fbThread != nil {
			if primaryButton(c.Key("fb-reply-send"), i18n.T("Reply")).Disabled(a.fbReplying || strings.TrimSpace(a.fbReplyText) == "").Clicked() {
				a.fbSendReply()
			}
		}
	})
}

// message is one message of a thread.
func fbMessage(c *ui.Context, key, who, when, body string, support bool) {
	t := c.Theme()
	k := tok(t)
	bg := k.Subtle
	if support {
		bg = k.AccentSoft
	}
	ui.Column(c.Key(key)).Gap(px(t, 4)).Padding(px(t, sp2), px(t, sp3)).Radius(px(t, radiusControl)).Background(bg).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).Children(func() {
			ui.Text(c, who).FontWeight(600).FontSize(px(t, fsCaption))
			caption(c, when)
		})
		ui.Text(c, body).Selectable()
	})
}

// previewDialog shows diagnostics.json as it would be sent now.
func (a *app) fbPreviewDialog(c *ui.Context) {
	if !a.fbPreviewOpen {
		return
	}
	t := c.Theme()
	dialogFrame(c, "fb-preview-dlg", &a.fbPreviewOpen, i18n.T("What is sent"), i18n.T("diagnostics.json, attached to your feedback when Include diagnostics is on."), px(t, 640), func() {
		ui.Text(c, a.fbPreview).Font("monospace").FontSize(px(t, 11)).Selectable()
	}, func() {
		ui.Box(c).Grow(1)
		if ui.Button(c.Key("fb-preview-copy"), i18n.T("Copy")).Clicked() {
			c.WriteClipboard(a.fbPreview)
			a.toast(i18n.T("Copied"))
		}
		if primaryButton(c.Key("fb-preview-done"), i18n.T("Done")).Clicked() {
			a.fbPreviewOpen = false
		}
	})
}
