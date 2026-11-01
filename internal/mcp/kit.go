package mcp

// The toolkit's tools (thread #135): the Voice isolator, Assets, the
// sample projects, voice management, the reference check and the story
// draft. Each calls an api.Server operation (internal/api/kit.go), as
// REST does; like studio.go's they touch files or the library on this
// Mac, so only clients on this Mac (or with the API key) may call them.
// kit_more.go has the small tools (design candidates, convert, models,
// watermark, probe, direction, rate fit, settings).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hehehai/AudioSmith/internal/api"
)

// KitTools are the tools of kit.go and kit_more.go, in the order they are
// added.
var KitTools = []string{"isolate_voice", "get_isolation", "list_assets", "list_samples", "open_sample",
	"update_voice", "duplicate_voice", "export_voice", "import_voice", "preview_voice", "check_reference", "write_story",
	"design_voice_candidates", "convert_voice", "download_model", "detect_watermark", "probe_media",
	"direct_performance", "check_line_fit", "get_settings"}

// outputMode is a call's output argument, or the setting.
func (s *Server) outputMode(arg string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(arg))
	if mode == "" {
		mode = s.o.Backend.Settings().API.Output()
	}
	if mode != "inline" && mode != "file" && mode != "both" {
		return "", errors.New("invalid_request: output is inline, file or both")
	}
	return mode, nil
}

// audio is audio for a result as mode asks: inline content and/or a
// file. data is the audio, or nil to read src; src is the file already
// on disk (returned as file in file mode when name is ""), else a copy
// named name is written to the agents' folder.
func (s *Server) audio(mode string, data []byte, src, mime, name string) (extra []sdk.Content, file, note string, err error) {
	if data == nil && (mode == "inline" || mode == "both") {
		fi, err := os.Stat(src)
		if err != nil {
			return nil, "", "", err
		}
		if fi.Size() <= maxInline {
			if data, err = os.ReadFile(src); err != nil {
				return nil, "", "", err
			}
		}
	}
	if mode == "inline" || mode == "both" {
		if len(data) == 0 || len(data) > maxInline {
			note, mode = "The audio is too long to send inline: its file is given instead.", "file"
		} else {
			extra = append(extra, &sdk.AudioContent{Data: data, MIMEType: mime})
		}
	}
	if mode == "file" || mode == "both" {
		switch {
		case name == "" && src != "":
			file = src
		default:
			if data == nil {
				if data, err = os.ReadFile(src); err != nil {
					return nil, "", "", err
				}
			}
			file, err = s.writeBytes(name, data)
		}
	}
	return extra, file, note, err
}

// writeBytes writes data to the agents' folder as name (a file name).
func (s *Server) writeBytes(name string, data []byte) (string, error) {
	dir := s.sandbox()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, api.SafeName(name, "audio", ""))
	tmp, err := os.CreateTemp(dir, ".audiosmith-*")
	if err != nil {
		return "", err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return dst, err
}

func (s *Server) isolateVoice(ctx context.Context, req *sdk.CallToolRequest, in api.IsolateRequest) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	x, err := s.o.API.StartIsolation(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	x.Next = "get_job with job_id " + x.JobID + " and wait_seconds, then get_isolation with isolation_id " + x.ID + " for the stems."
	return result(x)
}

func (s *Server) getIsolation(ctx context.Context, req *sdk.CallToolRequest, in api.IsolationQuery) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		if in.Stem != "" {
			return nil, nil, errors.New("invalid_request: stem needs isolation_id")
		}
		l, err := s.o.API.Isolations(ctx)
		if err != nil {
			return nil, nil, toolError(err)
		}
		return result(map[string]any{"count": len(l), "isolations": l})
	}
	x, err := s.o.API.Isolation(ctx, id)
	if err != nil {
		return nil, nil, toolError(err)
	}
	stem := strings.ToLower(strings.TrimSpace(in.Stem))
	if stem == "" {
		return result(x)
	}
	var file string
	for _, st := range x.Stems {
		if st.Name == stem {
			file = st.File
		}
	}
	if file == "" {
		return nil, nil, fmt.Errorf("not_found: isolation %s has no %s stem (yet): its state is %s; stems are vocals, background, clean", x.ID, stem, x.State)
	}
	mode, err := s.outputMode(in.Output)
	if err != nil {
		return nil, nil, err
	}
	extra, path, note, err := s.audio(mode, nil, file, "audio/wav", "")
	if err != nil {
		return nil, nil, err
	}
	return result(map[string]any{"isolation": x, "stem": stem, "file": path, "note": note}, extra...)
}

func (s *Server) listAssets(ctx context.Context, req *sdk.CallToolRequest, in api.AssetQuery) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	l, err := s.o.API.Assets(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(map[string]any{"count": len(l), "assets": l})
}

func (s *Server) listSamples(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	l, err := s.o.API.Samples(ctx)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(map[string]any{"count": len(l), "samples": l})
}

func (s *Server) openSample(ctx context.Context, req *sdk.CallToolRequest, in api.SampleOpen) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	o, err := s.o.API.OpenSample(ctx, in.ID)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(o)
}

func (s *Server) updateVoice(ctx context.Context, req *sdk.CallToolRequest, in api.VoiceUpdate) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	v, err := s.o.API.UpdateVoice(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(map[string]any{"voice": voiceOut(v)})
}

func (s *Server) duplicateVoice(ctx context.Context, req *sdk.CallToolRequest, in api.VoiceDuplicate) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	v, err := s.o.API.DuplicateVoice(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(map[string]any{"voice": voiceOut(v)})
}

func (s *Server) exportVoice(ctx context.Context, req *sdk.CallToolRequest, in api.VoiceExportRequest) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	out, err := s.o.API.ExportVoice(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(out)
}

func (s *Server) importVoice(ctx context.Context, req *sdk.CallToolRequest, in api.VoiceImportRequest) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	v, err := s.o.API.ImportVoice(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(v)
}

func (s *Server) previewVoice(ctx context.Context, req *sdk.CallToolRequest, in api.VoicePreviewQuery) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	mode, err := s.outputMode(in.Output)
	if err != nil {
		return nil, nil, err
	}
	p, err := s.o.API.VoicePreview(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	ext := strings.TrimPrefix(filepath.Ext(p.Name), ".")
	extra, file, note, err := s.audio(mode, p.Data, "", p.MIME, "preview-"+strings.TrimSuffix(p.Name, "."+ext)+"."+ext)
	if err != nil {
		return nil, nil, err
	}
	return result(map[string]any{"name": p.Name, "mime": p.MIME, "text": p.Text, "model": p.Model, "file": file, "note": note}, extra...)
}

func (s *Server) checkReference(ctx context.Context, req *sdk.CallToolRequest, in api.RefCheckRequest) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	out, err := s.o.API.CheckReference(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(out)
}

func (s *Server) writeStory(ctx context.Context, req *sdk.CallToolRequest, in api.StoryDraftRequest) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	out, err := s.o.API.WriteStory(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(out)
}

func (s *Server) addKitTools() {
	add := func(name, title, desc string) *sdk.Tool {
		return &sdk.Tool{Name: name, Title: title, Description: desc}
	}
	sdk.AddTool(s.srv, add("isolate_voice", "Isolate voices",
		"Voice isolator: separate the voices of an audio or video file from its music and effects, and denoise them. Returns isolation_id and job_id at once; get_job follows it, get_isolation gives the stems (vocals, background, clean)."), s.isolateVoice)
	sdk.AddTool(s.srv, add("get_isolation", "Read isolations",
		"The Voice isolator's isolations: without isolation_id all of them; with it one, its state and its stems' WAV paths; with stem, that stem's audio too."), s.getIsolation)
	sdk.AddTool(s.srv, add("list_assets", "List assets",
		"Assets: every file AudioSmith made or took in, newest first (speech takes, story and audiobook renders, exports, dubs, isolated stems, imported recordings), with its path and the tool that opens its source."), s.listAssets)
	sdk.AddTool(s.srv, add("list_samples", "List sample projects",
		"The sample projects of Home (a story for two voices, an audiobook chapter, a narration, a dubbed clip, a meeting transcript) and whether this build has them."), s.listSamples)
	sdk.AddTool(s.srv, add("open_sample", "Open a sample project",
		"Copy a sample project into the library (a new, editable copy) and say which tool reads it (get_longform, get_dub, export_transcript, list_assets)."), s.openSample)
	sdk.AddTool(s.srv, add("update_voice", "Rename or edit a voice",
		"Change a saved voice's name, style, language, tags or reference text, as My voices' editor does."), s.updateVoice)
	sdk.AddTool(s.srv, add("duplicate_voice", "Duplicate a voice",
		"Save a copy of a saved voice (its reference, sample and portrait; not its lock or consent)."), s.duplicateVoice)
	sdk.AddTool(s.srv, add("export_voice", "Export a voice file",
		"Write a saved voice as an .asvoice file (to share it or move it to another Mac); with include_reference it can be cloned elsewhere."), s.exportVoice)
	sdk.AddTool(s.srv, add("import_voice", "Import a voice file",
		"Add a voice from an .asvoice (or VoiceStudio .ovsvoice) file to My voices; its reference is checked and cleaned afterwards."), s.importVoice)
	sdk.AddTool(s.srv, add("preview_voice", "Hear a voice",
		"A short preview clip of a saved voice, a Discover voice (gallery_id) or a model's built-in speaker (model + speaker), inline and/or as a file. Nothing is generated: use it whenever the user wants to hear or audition a voice (before choosing, saving or using it), rather than generate_speech."), s.previewVoice)
	sdk.AddTool(s.srv, add("check_reference", "Check a clone recording",
		"Grade a recording as a voice-clone reference before clone_voice (good, fine or needs_work, with what to fix: noise, length, clipping, a text mismatch). Nothing is saved."), s.checkReference)
	sdk.AddTool(s.srv, add("write_story", "Write a story draft",
		"Stories' Help me write: the writing help's language model writes a short script for two or three voices from a one-line idea; with create: true the story is made and cast. Takes up to a minute."), s.writeStory)
	s.addMoreTools() // kit_more.go
}
