package transcribe

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
)

// TranscribeSides transcribes both capture channels locally, using one batch
// model and bounded requests. Preserve utterances during cross-talk instead of
// interleaving individual words from simultaneous sentences.
func (n *Nemo) TranscribeSides(ctx context.Context, stereo []byte, lang string) (string, error) {
	if len(stereo) == 0 || len(stereo)%4 != 0 {
		return "", fmt.Errorf("missing or incomplete source audio")
	}
	base, auth, cleanup, err := n.serve(ctx, n.config, n.config.Model, "source-attributed final pass")
	if err != nil {
		return "", err
	}
	defer cleanup()
	return joinSides(stereo, func(p []byte) (*Result, error) { return uploadPCM(ctx, base, auth, p, 16000, lang, "verbose_json") })
}
func joinSides(stereo []byte, recognize func([]byte) (*Result, error)) (string, error) {
	type turn struct {
		start      float64
		role, text string
	}
	var turns []turn
	const chunk = 4 * 16000 * 60
	for pos := 0; pos < len(stereo); pos += chunk {
		data := stereo[pos:min(len(stereo), pos+chunk)]
		for side, role := range []string{"Us", "Them"} {
			p := make([]byte, len(data)/2)
			for i := 0; i < len(data)/4; i++ {
				copy(p[2*i:2*i+2], data[4*i+side*2:4*i+side*2+2])
			}
			if bytes.Count(p, []byte{0}) == len(p) {
				continue
			}
			r, e := recognize(p)
			if e != nil {
				return "", e
			}
			if r.Text != "" && len(r.Words) == 0 {
				return "", fmt.Errorf("%s transcript has no timestamps", role)
			}
			for _, w := range r.Words {
				if w.Start < 0 || w.End < w.Start {
					return "", fmt.Errorf("invalid source timing")
				}
			}
			for _, seg := range segmentWords(r.Words) {
				turns = append(turns, turn{float64(pos)/64000 + seg.Start, role, seg.Text})
			}
		}
	}
	sort.SliceStable(turns, func(i, j int) bool { return turns[i].start < turns[j].start })
	var lines []string
	for _, t := range turns {
		lines = append(lines, t.role+": "+t.text)
	}
	return strings.Join(lines, "\n"), nil
}
