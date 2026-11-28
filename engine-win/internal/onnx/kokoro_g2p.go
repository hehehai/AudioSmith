package onnx

import (
	"strings"
	"unicode"
)

// defaultKokoroVocab matches the official hexgrad/Kokoro-82M v1.0 vocab table
// (config.json "vocab"). Token 0 is pad/BOS/EOS.
var defaultKokoroVocab = map[rune]int64{
	';': 1, ':': 2, ',': 3, '.': 4, '!': 5, '?': 6,
	'—': 9, '…': 10, '"': 11, '(': 12, ')': 13, '“': 14, '”': 15, ' ': 16,
	'\u0303': 17, 'ʣ': 18, 'ʥ': 19, 'ʦ': 20, 'ʨ': 21, 'ᵝ': 22, '\uab67': 23,
	'A': 24, 'I': 25, 'O': 31, 'Q': 33, 'S': 35, 'T': 36, 'W': 39, 'Y': 41, 'ᵊ': 42,
	'a': 43, 'b': 44, 'c': 45, 'd': 46, 'e': 47, 'f': 48, 'h': 50, 'i': 51, 'j': 52,
	'k': 53, 'l': 54, 'm': 55, 'n': 56, 'o': 57, 'p': 58, 'q': 59, 'r': 60, 's': 61,
	't': 62, 'u': 63, 'v': 64, 'w': 65, 'x': 66, 'y': 67, 'z': 68,
	'ɑ': 69, 'ɐ': 70, 'ɒ': 71, 'æ': 72, 'β': 75, 'ɔ': 76, 'ɕ': 77, 'ç': 78,
	'ɖ': 80, 'ð': 81, 'ʤ': 82, 'ə': 83, 'ɚ': 85, 'ɛ': 86, 'ɜ': 87, 'ɟ': 90,
	'ɡ': 92, 'ɥ': 99, 'ɨ': 101, 'ɪ': 102, 'ʝ': 103, 'ɯ': 110, 'ɰ': 111, 'ŋ': 112,
	'ɳ': 113, 'ɲ': 114, 'ɴ': 115, 'ø': 116, 'ɸ': 118, 'θ': 119, 'œ': 120, 'ɹ': 123,
	'ɾ': 125, 'ɻ': 126, 'ʁ': 128, 'ɽ': 129, 'ʂ': 130, 'ʃ': 131, 'ʈ': 132, 'ʧ': 133,
	'ʊ': 135, 'ʋ': 136, 'ʌ': 138, 'ɣ': 139, 'ɤ': 140, 'χ': 142, 'ʎ': 143, 'ʒ': 147,
	'ʔ': 148, 'ˈ': 156, 'ˌ': 157, 'ː': 158, 'ʰ': 162, 'ʲ': 164, '↓': 169, '→': 171,
	'↗': 172, '↘': 173, 'ᵻ': 177,
}

// commonEnglishIPA maps high-frequency English words to Kokoro IPA strings so
// everyday Windows PCs synthesize natural English speech without requiring
// espeak-ng to be installed.
var commonEnglishIPA = map[string]string{
	"a": "ɐ", "about": "əbˈAwt", "after": "ˈæftɚ", "again": "əɡˈɛn", "all": "ˈɔl",
	"also": "ˈɔlso", "am": "ˈæm", "an": "æn", "and": "ænd", "any": "ˈɛni",
	"are": "ɑɹ", "as": "æz", "at": "æt", "audio": "ˈɔdio", "audiosmith": "ˈɔdiosmˌɪθ",
	"back": "bˈæk", "be": "bi", "because": "bɪkˈɔz", "been": "bˈɪn", "before": "bɪfˈɔɹ",
	"being": "bˈiɪŋ", "best": "bˈɛst", "better": "bˈɛɾɚ", "between": "bɪtwˈin", "big": "bˈɪɡ",
	"both": "bˈOθ", "but": "bʌt", "by": "bˈI", "call": "kˈɔl", "can": "kæn",
	"check": "ʧˈɛk", "clean": "klˈin", "clear": "klˈɪɹ", "come": "kˈʌm", "computer": "kəmpjˈuɾɚ",
	"could": "kˈʊd", "cpu": "sˈi pˈi jˈu", "create": "kɹiˈAt", "data": "dˈAɾə", "day": "dˈA",
	"device": "dɪvˈIs", "did": "dˈɪd", "directml": "dɚˈɛkt ˌɛm ˈɛl", "do": "dˈu", "does": "dˈʌz",
	"done": "dˈʌn", "down": "dˈAwn", "each": "ˈiʧ", "engine": "ˈɛnʤən", "english": "ˈɪŋɡlɪʃ",
	"even": "ˈivən", "every": "ˈɛvɹi", "everyday": "ˈɛvɹidˌA", "fast": "fˈæst", "file": "fˈIl",
	"find": "fˈInd", "first": "fˈɜst", "for": "fɔɹ", "from": "fɹʌm", "full": "fˈʊl",
	"generate": "ʤˈɛnɚˌAt", "get": "ɡˈɛt", "give": "ɡˈɪv", "go": "ɡˈO", "good": "ɡˈʊd",
	"gpu": "ʤˈi pˈi jˈu", "great": "ɡɹˈAt", "had": "hˈæd", "has": "hˈæz", "have": "hˈæv",
	"he": "hi", "hear": "hˈɪɹ", "hello": "həlˈO", "help": "hˈɛlp", "her": "hɚ",
	"here": "hˈɪɹ", "hi": "hˈI", "high": "hˈI", "him": "hɪm", "his": "hɪz",
	"home": "hˈOm", "how": "hˈAw", "i": "ˈI", "if": "ɪf", "in": "ɪn",
	"inference": "ˈɪnfɚəns", "input": "ˈɪnpʊt", "into": "ˈɪntu", "is": "ɪz", "it": "ɪt",
	"its": "ɪts", "just": "ʤˈʌst", "keep": "kˈip", "know": "nˈO", "kokoro": "kˈOkɚˌO",
	"language": "lˈæŋɡwɪʤ", "last": "lˈæst", "let": "lˈɛt", "like": "lˈIk", "line": "lˈIn",
	"listen": "lˈɪsən", "little": "lˈɪɾəl", "live": "lˈɪv", "load": "lˈOd", "local": "lˈOkəl",
	"long": "lˈɔŋ", "look": "lˈʊk", "made": "mˈAd", "make": "mˈAk", "many": "mˈɛni",
	"may": "mˈA", "me": "mi", "memory": "mˈɛmɚi", "model": "mˈɑdəl", "more": "mˈɔɹ",
	"most": "mˈOst", "much": "mˈʌʧ", "music": "mjˈuzɪk", "must": "mˈʌst", "my": "mˈI",
	"name": "nˈAm", "natural": "nˈæʧɚəl", "need": "nˈid", "never": "nˈɛvɚ", "new": "njˈu",
	"next": "nˈɛkst", "no": "nˈO", "not": "nˈɑt", "now": "nˈAw", "number": "nˈʌmbɚ",
	"of": "ʌv", "off": "ˈɔf", "ok": "OkˈA", "okay": "OkˈA", "on": "ɑn",
	"one": "wˈʌn", "only": "ˈOnli", "onnx": "ˈɑnɪks", "open": "ˈOpən", "or": "ɔɹ",
	"other": "ˈʌðɚ", "our": "ˈAwɚ", "out": "ˈAwt", "output": "ˈAwtpʊt", "over": "ˈOvɚ",
	"own": "ˈOn", "part": "pˈɑɹt", "pc": "pˈi sˈi", "people": "pˈipəl", "play": "plˈA",
	"please": "plˈiz", "project": "pɹˈɑʤɛkt", "put": "pˈʊt", "quick": "kwˈɪk", "read": "ɹˈid",
	"ready": "ɹˈɛdi", "real": "ɹˈil", "really": "ɹˈɪli", "record": "ɹɪkˈɔɹd", "right": "ɹˈIt",
	"run": "ɹˈʌn", "running": "ɹˈʌnɪŋ", "runtime": "ɹˈʌntIm", "said": "sˈɛd", "same": "sˈAm",
	"say": "sˈA", "second": "sˈɛkənd", "see": "sˈi", "set": "sˈɛt", "she": "ʃi",
	"should": "ʃˈʊd", "show": "ʃˈO", "simple": "sˈɪmpəl", "small": "smˈɔl", "smooth": "smˈuð",
	"so": "sˈO", "some": "sˈʌm", "sound": "sˈAwnd", "speak": "spˈik", "speaker": "spˈikɚ",
	"speech": "spˈiʧ", "speed": "spˈid", "start": "stˈɑɹt", "still": "stˈɪl", "stop": "stˈɑp",
	"studio": "stˈudio", "such": "sˈʌʧ", "support": "səpˈɔɹt", "sure": "ʃˈʊɹ", "system": "sˈɪstəm",
	"take": "tˈAk", "talk": "tˈɔk", "tell": "tˈɛl", "test": "tˈɛst", "testing": "tˈɛstɪŋ",
	"text": "tˈɛkst", "than": "ðæn", "thank": "θˈæŋk", "thanks": "θˈæŋks", "that": "ðæt",
	"the": "ðə", "their": "ðɛɹ", "them": "ðɛm", "then": "ðˈɛn", "there": "ðɛɹ",
	"these": "ðˈiz", "they": "ðˈA", "thing": "θˈɪŋ", "think": "θˈɪŋk", "this": "ðˈɪs",
	"those": "ðˈOz", "three": "θɹˈi", "through": "θɹˈu", "time": "tˈIm", "to": "tu",
	"today": "tədˈA", "together": "təɡˈɛðɚ", "too": "tˈu", "track": "tɹˈæk", "try": "tɹˈI",
	"two": "tˈu", "under": "ˈʌndɚ", "up": "ˈʌp", "us": "ʌs", "use": "jˈuz",
	"used": "jˈuzd", "user": "jˈuzɚ", "using": "jˈuzɪŋ", "very": "vˈɛɹi", "video": "vˈɪdio",
	"voice": "vˈYs", "want": "wˈɑnt", "was": "wʌz", "water": "wˈɔɾɚ", "wave": "wˈAv",
	"way": "wˈA", "we": "wi", "welcome": "wˈɛlkəm", "well": "wˈɛl", "were": "wɚ",
	"what": "wˈʌt", "when": "wˈɛn", "where": "wˈɛɹ", "which": "wˈɪʧ", "while": "wˈIl",
	"who": "hˈu", "why": "wˈI", "will": "wˈɪl", "windows": "wˈɪndOz", "with": "wɪð",
	"without": "wɪðˈAwt", "word": "wˈɜd", "work": "wˈɜk", "working": "wˈɜkɪŋ", "works": "wˈɜks",
	"world": "wˈɜld", "would": "wˈʊd", "write": "ɹˈIt", "year": "jˈɪɹ", "yes": "jˈɛs",
	"you": "ju", "your": "jʊɹ", "zero": "zˈɪɹO",
}

var digitWords = map[rune]string{
	'0': "zero", '1': "one", '2': "two", '3': "three", '4': "four",
	'5': "five", '6': "six", '7': "seven", '8': "eight", '9': "nine",
}

// Phonemize converts plain text (or raw IPA wrapped in /.../) into a Kokoro
// IPA phoneme string without requiring external espeak-ng binaries.
func Phonemize(text string) string {
	s := strings.TrimSpace(text)
	if s == "" {
		return ""
	}
	// Explicit IPA passthrough: /həlˈO wˈɜld/
	if len(s) >= 2 && s[0] == '/' && s[len(s)-1] == '/' {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	// If text already contains IPA-specific symbols, pass through directly.
	if looksLikeIPA(s) {
		return s
	}

	var out strings.Builder
	var word strings.Builder
	flushWord := func() {
		if word.Len() == 0 {
			return
		}
		w := strings.ToLower(word.String())
		word.Reset()
		if ipa, ok := commonEnglishIPA[w]; ok {
			out.WriteString(ipa)
		} else {
			out.WriteString(englishWordToIPA(w))
		}
	}

	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || r == '\'':
			word.WriteRune(r)
		case r >= '0' && r <= '9':
			flushWord()
			if out.Len() > 0 && !strings.HasSuffix(out.String(), " ") {
				out.WriteByte(' ')
			}
			w := digitWords[r]
			if ipa, ok := commonEnglishIPA[w]; ok {
				out.WriteString(ipa)
			} else {
				out.WriteString(englishWordToIPA(w))
			}
			out.WriteByte(' ')
		case unicode.IsSpace(r):
			flushWord()
			if out.Len() > 0 && !strings.HasSuffix(out.String(), " ") {
				out.WriteByte(' ')
			}
		case r == '.' || r == ',' || r == '!' || r == '?' || r == ';' || r == ':':
			flushWord()
			out.WriteRune(r)
		case r == '-' || r == '—':
			flushWord()
			if out.Len() > 0 && !strings.HasSuffix(out.String(), " ") {
				out.WriteByte(' ')
			}
		default:
			flushWord()
		}
	}
	flushWord()
	return strings.TrimSpace(out.String())
}

func looksLikeIPA(s string) bool {
	for _, r := range s {
		switch r {
		case 'ə', 'ɪ', 'ʊ', 'æ', 'ɑ', 'ɔ', 'ɛ', 'ɜ', 'ʌ', 'ʃ', 'ʒ', 'θ', 'ð', 'ŋ', 'ɹ', 'ˈ', 'ˌ', 'ʧ', 'ʤ':
			return true
		}
	}
	return false
}

// englishWordToIPA is a lightweight rule-based English G2P fallback for words
// not in commonEnglishIPA.
func englishWordToIPA(w string) string {
	w = strings.Trim(w, "'")
	if w == "" {
		return ""
	}
	if ipa, ok := commonEnglishIPA[w]; ok {
		return ipa
	}
	// Strip simple contractions / possessive 's
	if base, ok := strings.CutSuffix(w, "'s"); ok && base != "" {
		return englishWordToIPA(base) + "z"
	}

	var b strings.Builder
	n := len(w)
	i := 0
	for i < n {
		rem := w[i:]
		switch {
		case strings.HasPrefix(rem, "tion"):
			b.WriteString("ʃən")
			i += 4
		case strings.HasPrefix(rem, "sion"):
			b.WriteString("ʒən")
			i += 4
		case strings.HasPrefix(rem, "igh"):
			b.WriteByte('I')
			i += 3
		case strings.HasPrefix(rem, "ing") && i+3 == n:
			b.WriteString("ɪŋ")
			i += 3
		case strings.HasPrefix(rem, "ch"):
			b.WriteRune('ʧ')
			i += 2
		case strings.HasPrefix(rem, "sh"):
			b.WriteRune('ʃ')
			i += 2
		case strings.HasPrefix(rem, "th"):
			b.WriteRune('θ')
			i += 2
		case strings.HasPrefix(rem, "ph"):
			b.WriteByte('f')
			i += 2
		case strings.HasPrefix(rem, "wh"):
			b.WriteByte('w')
			i += 2
		case strings.HasPrefix(rem, "ng"):
			b.WriteRune('ŋ')
			i += 2
		case strings.HasPrefix(rem, "ck"):
			b.WriteByte('k')
			i += 2
		case strings.HasPrefix(rem, "qu"):
			b.WriteString("kw")
			i += 2
		case strings.HasPrefix(rem, "ee") || strings.HasPrefix(rem, "ea"):
			b.WriteByte('i')
			i += 2
		case strings.HasPrefix(rem, "oo"):
			b.WriteByte('u')
			i += 2
		case strings.HasPrefix(rem, "ai") || strings.HasPrefix(rem, "ay"):
			b.WriteByte('A')
			i += 2
		case strings.HasPrefix(rem, "ou") || strings.HasPrefix(rem, "ow"):
			b.WriteString("Aw")
			i += 2
		case strings.HasPrefix(rem, "oi") || strings.HasPrefix(rem, "oy"):
			b.WriteByte('Y')
			i += 2
		case strings.HasPrefix(rem, "oa"):
			b.WriteByte('O')
			i += 2
		case strings.HasPrefix(rem, "ar"):
			b.WriteString("ɑɹ")
			i += 2
		case strings.HasPrefix(rem, "or"):
			b.WriteString("ɔɹ")
			i += 2
		case strings.HasPrefix(rem, "er") || strings.HasPrefix(rem, "ir") || strings.HasPrefix(rem, "ur"):
			b.WriteRune('ɚ')
			i += 2
		case strings.HasPrefix(rem, "ed") && i+2 == n && n > 3:
			b.WriteByte('d')
			i += 2
		default:
			ch := w[i]
			// Silent trailing 'e' after consonant in words >= 3 chars
			if ch == 'e' && i == n-1 && n >= 3 && !isVowel(w[i-1]) {
				i++
				continue
			}
			switch ch {
			case 'a':
				if hasSilentE(w, i) {
					b.WriteByte('A')
				} else {
					b.WriteRune('æ')
				}
			case 'e':
				b.WriteRune('ɛ')
			case 'i':
				if hasSilentE(w, i) {
					b.WriteByte('I')
				} else {
					b.WriteRune('ɪ')
				}
			case 'o':
				if hasSilentE(w, i) {
					b.WriteByte('O')
				} else {
					b.WriteRune('ɑ')
				}
			case 'u':
				if hasSilentE(w, i) {
					b.WriteByte('u')
				} else {
					b.WriteRune('ʌ')
				}
			case 'y':
				if i == n-1 {
					b.WriteByte('i')
				} else {
					b.WriteByte('j')
				}
			case 'c':
				if i+1 < n && (w[i+1] == 'e' || w[i+1] == 'i' || w[i+1] == 'y') {
					b.WriteByte('s')
				} else {
					b.WriteByte('k')
				}
			case 'g':
				b.WriteRune('ɡ')
			case 'j':
				b.WriteRune('ʤ')
			case 'q':
				b.WriteByte('k')
			case 'r':
				b.WriteRune('ɹ')
			case 'x':
				b.WriteString("ks")
			case 'b', 'd', 'f', 'h', 'k', 'l', 'm', 'n', 'p', 's', 't', 'v', 'w', 'z':
				b.WriteByte(ch)
			}
			i++
		}
	}
	return b.String()
}

func isVowel(b byte) bool {
	return b == 'a' || b == 'e' || b == 'i' || b == 'o' || b == 'u'
}

func hasSilentE(w string, idx int) bool {
	return idx+2 == len(w)-1 && !isVowel(w[idx+1]) && w[len(w)-1] == 'e'
}

// TokenizeKokoro maps an IPA phoneme string into Kokoro token IDs using vocab
// (or defaultKokoroVocab if vocab is nil), wrapped with leading and trailing 0.
func TokenizeKokoro(phonemes string, vocab map[rune]int64) []int64 {
	if len(vocab) == 0 {
		vocab = defaultKokoroVocab
	}
	ids := make([]int64, 0, len(phonemes)+2)
	ids = append(ids, 0)
	for _, r := range phonemes {
		if id, ok := vocab[r]; ok {
			ids = append(ids, id)
		}
	}
	ids = append(ids, 0)
	return ids
}
