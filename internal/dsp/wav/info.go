package wav

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"slices"
)

// AppendInfo adds a LIST/INFO chunk with tags (four-letter INFO ids:
// "ICMT" comment, "IART" artist, "INAM" title) at the end of the RIFF
// WAV file at path and fixes the RIFF size: the credit an export must
// carry (thread #171). Readers find it after the samples (samplesEnd).
// RF64 files and files near 4 GiB are left as they are (an error).
func AppendInfo(path string, tags map[string]string) error {
	if len(tags) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 12)
	if _, err := io.ReadFull(f, head); err != nil {
		return err
	}
	if string(head[:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		return errors.New("wav: not a RIFF WAVE file")
	}
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	size := fi.Size()
	var body []byte
	body = append(body, "INFO"...)
	ids := make([]string, 0, len(tags))
	for id := range tags {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if len(id) != 4 || tags[id] == "" {
			continue
		}
		v := append([]byte(tags[id]), 0)
		body = append(body, id...)
		body = binary.LittleEndian.AppendUint32(body, uint32(len(v)))
		body = append(body, v...)
		if len(v)%2 == 1 {
			body = append(body, 0)
		}
	}
	chunk := append([]byte("LIST"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...)
	chunk = append(chunk, body...)
	pad := size % 2 // chunks start at even offsets
	total := size + pad + int64(len(chunk))
	if total-8 > 0xFFFFFFFF {
		return errors.New("wav: too large for a RIFF chunk")
	}
	if pad == 1 {
		chunk = append([]byte{0}, chunk...)
	}
	if _, err := f.WriteAt(chunk, size); err != nil {
		return err
	}
	if _, err := f.WriteAt(binary.LittleEndian.AppendUint32(nil, uint32(total-8)), 4); err != nil {
		return err
	}
	return f.Sync()
}
