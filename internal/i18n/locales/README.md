# UI catalogues

`<tag>.json` maps each English source text (the key; `context\u0004text`
for `i18n.X`) to its translation. `en.json` stays empty. Every locale has
every key the sources use (`TestKeyParity`), with the same format verbs
and the same markup: `{name}`, `[pause …]`, `[[`/`]]`, `[voice:`,
`[emphasis]`/`[spell]`/`[laughter]`, `` `code` `` spans and line breaks.
Labels never end in "…" (`TestNoEllipsisLabels`).

## Japanese (ja) style and glossary

Thread #173. Style follows Apple's macOS Japanese: sentences in です/ます,
short noun or verb labels for buttons and menus (書き出す, 取り消す, 設定),
katakana loanwords where Apple uses them. No space between Japanese and
Latin words or numbers (「Macで」, 「3件」, 「APIキー」); full-width
（）：、。 and 「」 for quoted UI names (「設定」→「モデルとネットワーク」).
Counts never take a plural form: `i18n.N` uses the "other" text.

| English | 日本語 | note |
|---|---|---|
| voice (a saved/built-in voice) | ボイス | 声 only for a person's real voice (あなたの声, 話者自身の声) |
| My voices / Discover voices / Mine / Discover | マイボイス / ボイスを探す / マイボイス / 探す | |
| take | テイク | |
| Text to speech | テキスト読み上げ | tile: 読み上げ |
| Speech to text, Transcribe, transcript | 文字起こし | |
| Dub, dubbing | 吹き替え | verb: 吹き替える |
| Voice isolator, Isolate | ボイス分離, 分離 | stems: ステム |
| Stories | ストーリー | |
| Audiobook | オーディオブック | |
| Studio | スタジオ | |
| Clone, Quick clone | クローン, クイッククローン | |
| Design (a voice) | デザイン, ボイスデザイン | |
| reference (clip) | 参照音声, 参照クリップ | |
| Cast (noun), cast (verb) | キャスト, 配役する | Auto-cast: 自動キャスト |
| narrator, narration | ナレーター, 地の文 / ナレーション | |
| speaker, character | 話者, キャラクター | |
| dialogue passage / whole dialogue | 会話パート / 会話全体 | the markup tag stays `[dialogue]` |
| direction (performance note), AI director | 演技指示 / 演出, AIディレクター | |
| Watermark | 透かし (不可視の透かし) | |
| Assets | 素材 | |
| Projects, Library | プロジェクト, ライブラリ | |
| Export, Import | 書き出す, 読み込む | |
| Render | レンダリング | |
| Preview (listen) | 試聴 | |
| Dictation | 音声入力 | as macOS |
| Writing help | 文章アシスト | |
| Integrations, Developers | 連携, 開発者 | |
| Models & network | モデルとネットワーク | |
| agent | エージェント | |
| Keychain, Finder | キーチェーン, Finder | |
| Local API | ローカルAPI | |
| licence | ライセンス | |
| workflow, step, item | ワークフロー, ステップ, 項目 | |
| Undo, Redo, Cut, Copy, Paste | 取り消す, やり直す, カット, コピー, ペースト | |

Model names (`model name\u0004…`) are translated as short descriptive
names; brand and product names stay as they are (AudioSmith, VOICEVOX,
AivisSpeech, Qwen3, Kokoro, ffmpeg, Twilio, Hugging Face…).
