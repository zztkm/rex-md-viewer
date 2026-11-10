# rex md viewer

Rex で Markdown ファイルをプレビューするためのツール。

影響を受けたプロジェクト: https://github.com/almonk/rexpad.git

## インストール

ビルドには Go 1.27 以降が必要です。
表示には Kitty Graphics Protocol 対応の端末が必要で、分割表示には Rex と `rex` CLI を使用します。
日本語を表示する場合は、日本語対応フォントも必要です。
初期版の実機確認対象は macOS の Rex です。
ブラウザ、Web サーバー、Node.js は不要です。

```bash
go install github.com/zztkm/rex-md-viewer@latest
```

`go install` のインストール先（通常は `~/go/bin`）を `PATH` に追加してください。

## Usage

```bash
# 右側にプレビューを開く。繰り返しても同じ Viewer を再利用する
rex-md-viewer view README.md

# 現在の端末に表示する（q または Ctrl-C で終了）
rex-md-viewer view --in-place README.md

# 分割表示を閉じる
rex-md-viewer hide README.md

# Rex に接続せず PNG を生成する
rex-md-viewer render -o preview.png testdata/sample.md

# Agent Skill をインストールする
rex-md-viewer skill install --dir ~/.pi/agent/skills
```

### フォントの設定

環境変数で本文と太字のフォントを個別に設定できます。
Rex の端末設定のフォントを引き継ぐ方式ではありません。

```bash
export REX_MD_VIEWER_FONT="/path/to/regular.ttf"
export REX_MD_VIEWER_FONT_BOLD="/path/to/bold.ttf"
rex-md-viewer view README.md
```

選択順は本文と太字それぞれについて、CLI オプション → 環境変数 → SF Pro Text → Inter → 同梱の Go フォントとします。

| 用途 | CLI オプション | 環境変数 | 最終フォールバック |
| --- | --- | --- | --- |
| 本文 | `--font PATH` | `REX_MD_VIEWER_FONT` | Go Regular |
| 見出しと太字 | `--font-bold PATH` | `REX_MD_VIEWER_FONT_BOLD` | Go Bold |

TTF、OTF、TTC に対応し、フォント名ではなくファイルパスを指定します。
SF Pro Text は `/Library/Fonts` と `~/Library/Fonts` の静的 OTF、Inter は Linux の既知のパスを探索します。
環境変数のパスが存在しない場合や読み込めない場合は、次の候補へ進みます。
一方、CLI で明示したパスの不備はエラーにします。
相対パスは呼び出し元の作業ディレクトリを基準に解決し、分割先にも解決済みの設定を渡します。
既存 Viewer のフォントを変更する場合は `hide` してから開き直してください。

基本フォントで描けない文字は、Noto Sans JP、macOS のヒラギノ、Linux の Noto Sans CJK など、既知のパスの日本語フォントで文字ごとに補います。
漢字、ひらがな、カタカナを基本フォントでもフォールバックでも描けない場合は、設定方法を示してエラーにします。
このため、同梱 Go フォントだけでも英語の文書は表示できますが、日本語の文書には日本語フォントが必要です。
日本語フォントは同梱せず、Go Regular、Go Bold とコードの ASCII 文字用の Go Mono のみを同梱します。

本文は画像として表示するため、端末標準の文字選択、コピー、検索は使えません。
自動更新は Markdown 本体が対象で、ローカル画像だけを変更した場合は Viewer で `r` を押してください。

## 開発と検証

```bash
make build
make check test

# 疑似端末で画像送信、更新、リサイズ、テーマ変更、終了時の復元を確認する
make smoke

# Rex 内でのみ実行する。テスト用ペインを作成し、終了時に閉じる
make smoke-rex

# Rex の入力エンコーダー経由でリンク操作を検証する（ブラウザは起動しない）
make smoke-links-rex
```

スモークテストには Python 3 が必要です。
検証内容と制限は [docs/testing.md](docs/testing.md) に記載しています。

## ライセンス

本プロジェクトは [MIT License](LICENSE) で公開しています。
第三者由来のコードと同梱フォントには、それぞれのライセンスが適用されます。
詳細は [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) を参照してください。
バイナリを再配布する際は、`LICENSE` と `THIRD_PARTY_NOTICES.md` を同梱してください。

