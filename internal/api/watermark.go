package api

// POST /api/watermark/detect: is an uploaded file watermarked by AudioSeal,
// and by AudioSmith? (thread #14; the engine's watermark.detect.)

import (
	"net/http"
	"os"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/watermark"
)

// WatermarkReport is the detection with the message read as AudioSmith's.
type WatermarkReport struct {
	proto.WatermarkDetectResult
	// AudioSmith: the mark is AudioSmith's (its marker byte, decoded with
	// confidence); Install is then the install id that made it.
	AudioSmith bool `json:"audioSmith"`
	Install    int  `json:"install,omitempty"`
}

// Report reads a detection.
func Report(r proto.WatermarkDetectResult) WatermarkReport {
	rep := WatermarkReport{WatermarkDetectResult: r}
	if ours, id := watermark.Decode(r.Message); ours && r.Detected && r.Confidence >= 0.2 {
		rep.AudioSmith, rep.Install = true, int(id)
	}
	return rep
}

func (s *Server) watermarkDetect(w http.ResponseWriter, r *http.Request) {
	if err := s.engineGate(); err != nil {
		writeError(w, err)
		return
	}
	path, _, err := s.formFile(r, "file")
	if err != nil {
		writeError(w, err)
		return
	}
	defer os.Remove(path)
	rep, err := s.DetectWatermark(r.Context(), path)
	writeResult(w, http.StatusOK, rep, err)
}
