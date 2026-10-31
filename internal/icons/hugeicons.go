package icons

import "strings"

// Play and Pause are Hugeicons' "play" and "pause" (the free stroke-rounded
// set) rather than Solar's: every audio playback button shows them. They
// come from the npm package @iconify-json/hugeicons at HugeiconsVersion
// (https://icon-sets.iconify.design/hugeicons/). Hugeicons is under the MIT
// licence; the notice is in NOTICE, THIRD-PARTY-NOTICES.txt and Settings →
// About. This file is kept by hand (internal/icons/gen writes only Solar):
// Linear is the package's body verbatim; the free set has no solid
// variant, so Bold is the same shape filled as well as stroked.

// HugeiconsVersion is the version of @iconify-json/hugeicons Play and
// Pause come from.
const HugeiconsVersion = "1.2.35"

var (
	// Play is Hugeicons' "play".
	Play = hugeicon("play",
		"<path fill=\"none\" stroke=\"currentColor\" stroke-linejoin=\"round\" stroke-width=\"1.5\" d=\"M18.89 12.846c-.353 1.343-2.023 2.292-5.364 4.19c-3.23 1.835-4.845 2.752-6.146 2.384a3.25 3.25 0 0 1-1.424-.841C5 17.614 5 15.743 5 12s0-5.614.956-6.579a3.25 3.25 0 0 1 1.424-.84c1.301-.37 2.916.548 6.146 2.383c3.34 1.898 5.011 2.847 5.365 4.19a3.3 3.3 0 0 1 0 1.692Z\"/>")
	// Pause is Hugeicons' "pause".
	Pause = hugeicon("pause",
		"<path fill=\"none\" stroke=\"currentColor\" stroke-width=\"1.5\" d=\"M4 7c0-1.414 0-2.121.44-2.56C4.878 4 5.585 4 7 4s2.121 0 2.56.44C10 4.878 10 5.585 10 7v10c0 1.414 0 2.121-.44 2.56C9.122 20 8.415 20 7 20s-2.121 0-2.56-.44C4 19.122 4 18.415 4 17zm10 0c0-1.414 0-2.121.44-2.56C14.878 4 15.585 4 17 4s2.121 0 2.56.44C20 4.878 20 5.585 20 7v10c0 1.414 0 2.121-.44 2.56c-.439.44-1.146.44-2.56.44s-2.121 0-2.56-.44C14 19.122 14 18.415 14 17z\"/>")
)

// hugeicon is a Hugeicons icon: its Bold variant fills the stroked shape.
func hugeicon(name, body string) Icon {
	return Icon{Name: "hugeicons:" + name, Linear: parse(body), Bold: parse(strings.Replace(body, `fill="none"`, `fill="currentColor"`, 1))}
}
