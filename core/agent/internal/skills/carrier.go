// SPDX-License-Identifier: Apache-2.0

package skills
















import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)














var carrierLineRe = regexp.MustCompile(`\A<!-- baton-code: [^\r\n]*-->[ \t]*\z`)







func splitKeepingTerminators(text string) [][2]string {
	out := [][2]string{}
	for i := 0; i < len(text); {
		j := strings.IndexAny(text[i:], "\r\n")
		if j < 0 {
			out = append(out, [2]string{text[i:], ""})
			break
		}
		j += i
		term := text[j : j+1]
		if text[j] == '\r' && j+1 < len(text) && text[j+1] == '\n' {
			term = text[j : j+2]
		}
		out = append(out, [2]string{text[i:j], term})
		i = j + len(term)
	}
	return out
}








func StripCode(text string) string {
	var b strings.Builder
	for _, seg := range splitKeepingTerminators(text) {
		if !carrierLineRe.MatchString(seg[0]) {
			b.WriteString(seg[0])
		}
		b.WriteString(seg[1])
	}
	return b.String()
}







func ContentDigest(raw []byte) string {
	text := string(raw)
	text = strings.TrimPrefix(text, "\ufeff")
	text = StripCode(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")



	text = strings.TrimRight(text, "\n \t") + "\n"
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}






func ReadCode(raw []byte) string {
	last := ""
	for _, seg := range splitKeepingTerminators(string(raw)) {
		if carrierLineRe.MatchString(seg[0]) {
			inner := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(seg[0]), "<!-- baton-code:"), "-->")
			last = strings.TrimSpace(inner)
		}
	}
	return last
}






























func InjectCode(raw []byte, codeID string) ([]byte, error) {
	if strings.ContainsAny(codeID, "\r\n") {
		return nil, fmt.Errorf("skills: a code id cannot contain a line break")
	}
	before := ContentDigest(raw)
	body := StripCode(string(raw))
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	out := []byte(body + "<!-- baton-code: " + codeID + " -->\n")
	if after := ContentDigest(out); after != before {
		return nil, fmt.Errorf(
			"skills: injecting the code moved content_digest (%s -> %s); nothing was written, "+
				"and this is a defect in the injector rather than in the skill", before, after)
	}
	return out, nil
}
