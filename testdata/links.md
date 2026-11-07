# 外部リンクの操作確認

この文書の HTTP/HTTPS の文字リンクを Cmd＋左クリックすると、既定のブラウザへ渡します。
通常のクリック、余白、コード、ローカルファイルのリンクでは開きません。

[リンク A](https://example.com/a?x=1&y=2#section) と [リンク B][reference]

[折り返しを確認するための長いリンク文字列です。ペインを狭くして、各行から同じリンク先を開けることを確認します。](https://example.com/wrapped)

小さな隣接リンク: [i](https://example.com/i)[l](https://example.com/l)

装飾: [**太字** と `インラインコード`](https://example.com/styled)

自動リンク: <https://example.com/auto> と https://example.com/bare

> [引用のリンク](https://example.com/quote)

- [ ] [未完了のリンク](https://example.com/pending)
- [~] [作業中のリンク](https://example.com/active)
- [x] [完了した項目のリンク](https://example.com/done)

| 左寄せ | 中央寄せ | 右寄せ |
| :--- | :---: | ---: |
| [左](https://example.com/left) | [中央](https://example.com/center) | [右](https://example.com/right) |

## 開かない対象

[相対パス](sample.md)、[文書内アンカー](#外部リンクの操作確認)、[ローカルファイル](file:///tmp/example)、[メール](mailto:example@example.com)

`https://example.com/inline-code`

```text
https://example.com/code-block
```

<a href="https://example.com/html">HTML のリンク</a>

画像と代替表示もクリック対象にしません。

[![ローカル画像](example.png)](https://example.com/image)

[![読み込めない画像](missing.png)](https://example.com/missing-image)

[reference]: https://example.com/reference
