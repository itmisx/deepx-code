<div align="center">

# deepx-code

**DeepSeek ネイティブ・OpenAI 互換のターミナル向けコーディングエージェント —— 単一バイナリ・キャッシュフレンドリー・コードグラフとローカル OCR を内蔵**

**DeepSeek・Xiaomi MiMo・Kimi・Qwen をプリセット、任意の OpenAI 互換モデルにも対応**

[![Go](https://img.shields.io/badge/built%20with-Go-00ADD8?logo=go&logoColor=white)](https://go.dev) [![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE) [![Release](https://img.shields.io/github/v/release/itmisx/deepx-code?color=success)](https://github.com/itmisx/deepx-code/releases) [![Downloads](https://img.shields.io/github/downloads/itmisx/deepx-code/total?color=success&label=downloads)](https://github.com/itmisx/deepx-code/releases) [![Stars](https://img.shields.io/github/stars/itmisx/deepx-code?style=flat)](https://github.com/itmisx/deepx-code/stargazers) ![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)

[简体中文](README.md) · [English](README.en.md) · **日本語** · [한국어](README.ko.md)

![deepx-code demo](assets/demo.gif)

</div>

> [!TIP]
> **⚡ 長いセッションで実測 ~99% の prompt-cache ヒット**（実際のセッション：41,591 トークン中 41,472 がキャッシュ）。DeepSeek はキャッシュヒットの入力をキャッシュミスの数十分の一で課金するため（[公式料金](https://api-docs.deepseek.com/quick_start/pricing)）、長時間の実行でも繰り返しのコンテキストにほとんど課金されません。

---

## ✨ 特長

- **🦫 単一の Go バイナリ** —— Node / Python ランタイム不要、`curl` 一行でインストール、macOS / Linux / Windows 対応。
- **💰 キャッシュフレンドリーで長時間でも安い** —— DeepSeek のプレフィックスキャッシュを軸に設計（実測 ~99% ヒット）。ローカル意味ルーティングは毎ターン、遅延ゼロ・トークンゼロで起動。
- **🧭 コードグラフ内蔵（codegraph）** —— シンボル単位の定義ジャンプ / 呼び出し元 / インターフェース実装 / 影響範囲。Go は `go/types` で正確に解析し、リポジトリ全体の grep を置き換えます。
- **👀 ローカル画像 OCR（PaddleOCR）** —— スクリーンショットの文字をオフラインで読み取り、マルチモーダル API は不要。
- **📎 `@` ファイル / ディレクトリ参照** —— 入力欄で `@` を打つとローカルのファジー検索パスピッカーが開く。選択すると `@パス` がメッセージに挿入され、モデルは必要に応じて Read（ファイル）/ List（ディレクトリ）を呼ぶ。コンテキストを精密に渡せて、全部詰め込まなくて済む。
- **🧠 デュアルモデル自動ルーティング** —— 軽い処理は flash、複雑なタスクは自動で pro に昇格。`/model flash|pro` でモデル固定、`/auto` `/plan` `/review` でモード切替も可能。
- **🗂️ 逐次 Todo + 並列 Plan DAG** —— 多段階タスクは可視チェックリストで一歩ずつ。独立した並列タスクは DAG に分解してサブエージェントを並列実行。
- **🔁 再利用可能な Workflow** —— 繰り返し実行する複数エージェントのフローを JS スクリプトで固定化(`agent()` / `parallel()` / `pipeline()`):多視点レビュー、ファンアウト調査、パイプライン、空になるまでループなど。`/ultracode <説明>` でモデルが自動生成・保存、`/workflow <名前>` で実行。真の並列・中断後 resume・構造化出力はツールで強制・実行前に全フェーズを表示し所要時間をリアルタイム表示。Claude Code の workflow スクリプト規約に準拠し、スクリプトはそのまま相互利用可能。
- **💾 ロスレスなセッション永続化** —— gob が `tool_calls` / ツール結果 / `reasoning_content` を完全保持し、再起動後もシームレスに継続。ウィンドウが埋まると自動で階層圧縮。
- **🔌 MCP + Skill エコシステム** —— MCP ネイティブ対応。Claude の skill ディレクトリと互換で、既存の skill をそのまま再利用。
- **🛡️ レビューモード** —— ファイル書き込み / Shell 実行はデフォルトで人間の確認を要求。
- **🧱 ネイティブ OS レベルサンドボックス** —— 既定の `native` は OS 隔離（macOS は Seatbelt、Linux は bubblewrap — 書き込みを workspace に限定 + プロセス隔離;OS 機構が無い環境はソフトポリシーのブラックリストにフォールバック）。`docker` コンテナ隔離や `off` も選択可。コンテナ無しでも agent に安全境界を引ける。
- **🎛️ 作業モード（working mode）** —— 1 コマンドで方法論を固定：`karpathy`（実用重視）/ `openspec`（仕様駆動）/ `superpowers`（全工程を厳格に）。3 モードは排他的で、1 つ選ぶと他 2 つの skill を無効化し方法論の混在を防ぐ。セッションに保存され、毎ターン履歴を汚さずプロンプトを注入。
- **⚡ 非対話 `exec` モード** —— `deepx exec "タスク"` は一度だけ実行して結果を stdout に直接出力。パイプで入力、出力をリダイレクト、スクリプト / CI / cron に組み込み可能で、**TUI に入る必要なし**(下記参照)。

## 📊 Claude Code との比較

|                     | **deepx-code**                          | Claude Code              |
| :------------------ | :-------------------------------------- | :----------------------- |
| 配布                | 単一 Go バイナリ、`curl` 一行            | Node（npm）              |
| オープンソース      | ✅ MIT                                  | ❌ クローズド            |
| モデル              | DeepSeek / Xiaomi MiMo（OpenAI 互換、設定時にプロバイダを選択、flash/pro 自動ルーティング） | Anthropic Claude   |
| コスト              | 長いセッションで ~99% キャッシュヒット   | サブスク / Claude API 従量 |
| コードグラフ内蔵    | ✅ codegraph（Go は `go/types` で正確）  | ❌（grep / 検索）        |
| ローカル・オフライン OCR | ✅ PaddleOCR                        | ❌（画像はクラウドのマルチモーダル） |
| MCP                 | ✅                                      | ✅                       |
| Skill エコシステム  | ✅（Claude の skill ディレクトリを再利用） | ✅                     |

> [!NOTE]
> これはモデルの品質そのものを比べるものではありません。deepx-code のトレードオフは **コスト・オープンソース・単一バイナリ・内蔵コードグラフ・オフライン OCR** です。

## 🚀 クイックスタート

**1. インストール**

macOS / Linux(末尾の `&& exec $SHELL` は現在のシェルを再起動し、PATH に deepx をすぐ反映させます。rc の source や新しいターミナルを開く必要はありません):

```bash
curl -fsSL https://raw.githubusercontent.com/itmisx/deepx-code/main/scripts/install.sh | bash && exec $SHELL
```

Windows(PowerShell):

```powershell
irm https://raw.githubusercontent.com/itmisx/deepx-code/main/scripts/install.ps1 | iex
```

🇨🇳 中国本土のユーザーは **Gitee ミラー** で高速にインストールできます(ソース・バイナリとも Gitee から取得。以降の `deepx upgrade` も Gitee 経由):

macOS / Linux:

```bash
curl -fsSL https://gitee.com/itmisx/deepx-code/raw/main/scripts/install.sh | SOURCE=gitee bash && exec $SHELL
```

Windows PowerShell

```powershell
$env:SOURCE='gitee'; irm https://gitee.com/itmisx/deepx-code/raw/main/scripts/install.ps1 | iex
```

`~/.local/bin/deepx` にインストールされます。`deepx upgrade` でいつでも更新可能。

**2. ターミナルでプロジェクトに入って起動**

deepx は**ターミナルプログラム**です。ターミナルを開き、プロジェクトに `cd` して `deepx` を実行すると対話 UI に入ります。

- どのターミナルでも OK:macOS の Terminal / iTerm2、Linux のターミナル、Windows Terminal / PowerShell。
- **VS Code 内蔵ターミナル**もおすすめ(`Terminal → New Terminal`、または `` Ctrl+` ``):開いているプロジェクトのディレクトリにいるので、`deepx` がそのプロジェクトに対して動き、編集はエディタに即座に反映されます。

```bash
cd <あなたのプロジェクト>   # VS Code の内蔵ターミナルなら通常すでにプロジェクト直下
deepx                       # 対話型 TUI に入る
```

**3. 設定**

| 項目          | 方法                                                         |
| :------------ | :----------------------------------------------------------- |
| プロバイダ & Key | 初回起動時のウィザードで：**←/→ でプロバイダ（DeepSeek / Xiaomi MiMo）を選び、対応する API Key を入力**し `~/.deepx/model.yaml` に保存。各プロバイダに flash/pro の既定モデルと 1M コンテキストを用意（DeepSeek `deepseek-v4-flash` / `-pro`、MiMo `mimo-v2.5` / `-pro`）。`/config` で再設定。 |
| 手動上書き    | `~/.deepx/model.yaml` を直接編集し、role（flash/pro）ごとに `base_url` / `model` / `api_key` / `max_tokens` / `context_window` を上書き可能。flash と pro で別プロバイダも指定できる。 |
| 複数プロバイダ切替 | `/config` のたびに設定がプロバイダ名(deepseek/mimo/kimi/qwen/custom)で `~/.deepx/provider.yaml` に保存される。`/provider` で設定済みプロバイダ間をワンタップ切替(そのプロバイダの flash/pro を `model.yaml` に書き戻す)、Key の再入力不要。 |
| Skill         | `<ワークスペース>/.deepx/skills/` に配置、または `~/.claude/skills/` などを再利用。 |
| MCP           | TUI 内で `/mcp-add` で追加、`/mcp-list` で一覧。              |

## ⚡ 非対話実行（`deepx exec`）

フル TUI に入らず deepx をスクリプトに組み込みたいときは `deepx exec "<タスク>"` を使います。タスクを実行し、結果をそのままターミナル(stdout)に出力して終了します。結果のみ、途中の出力はありません。

```bash
deepx exec "README の機能リストを英語に翻訳して README.en.md に書き込む"
```

パイプ入力にも対応(`cat error.log | deepx exec "このエラーを分析して"`)。先に対話型 `deepx` で API キーを設定しておいてください。

## 🧠 仕組み

<details>
<summary><b>セッション永続化（gob バイナリ、ロスレス再開）</b></summary>

```
~/.deepx/sessions/<sha1(workspace)[:16]>/
├── meta.json          # ワークスペースのメタ情報
├── state.json         # 圧縮状態 + 使用量スナップショット
├── YYYY-MM-DD.jsonl   # テキストログ（Memory 検索用）
└── history.gob        # 完全なバイナリ履歴
```

| 形式               | 保存内容                                                                | 用途                          |
| :----------------- | :---------------------------------------------------------------------- | :---------------------------- |
| `history.gob`      | system + user + assistant（`tool_calls`・ツール結果・`reasoning_content` を含む） | **再起動からの復元、シームレス継続** |
| `YYYY-MM-DD.jsonl` | user / assistant のプレーンテキスト                                     | Memory ツールの検索           |

再起動時はまず gob を読み込み、失敗時は JSONL にフォールバック。アップグレードや skill 変更で system prompt が変わった場合、gob 復元時に現行版へ透過的に置換し、キャッシュのプレフィックスを安定させます。

</details>

<details>
<summary><b>セッション圧縮（階層 + サマリ統合）</b></summary>

コンテキストウィンドウの 70% を超えると自動で発動：末尾に約 20K トークンを階層的に残し、古い内容は LLM が一貫したサマリに圧縮して既存サマリと統合します。gob も更新されるため、再起動後も整合します。

</details>

<details>
<summary><b>タスク計画：Todo（逐次）vs Plan DAG（並列）</b></summary>

- **Todo** —— 多段階・逐次・文脈依存の作業（ゼロからのアプリ構築など）：モデルが可視チェックリストに手順を並べ、一つずつチェックしながら自分で実行し、進捗をリアルタイムに見せます。
- **CreatePlan（Plan DAG）** —— 本当に並列で独立したファンアウト：DAG に分解し、依存順に並列サブエージェントを起動。各ノードが flash / pro を選び、最後に集約します。

```
CreatePlan
  ├─ plan-1: Read  (flash) ─────┐
  ├─ plan-2: Read  (flash) ─────┤
  ├─ plan-3: Grep  (flash) ─────┤
  └─ plan-4: Write (pro)   ─────┘ depends_on: [1,2,3]
```

</details>

<details>
<summary><b>ローカル OCR（画像読み取りを補完）</b></summary>

画像を貼り付ける／パスを渡すと、LLM が `OCR` ツール（PaddleOCR PP-OCRv5）で文字を読み取ります。初回に OCR モデル（~37MB）と ONNX runtime をダウンロードし、以降は **オフラインで数秒** で応答。マルチモーダル API なしでも、エラーのスクリーンショットや UI モックをエージェントに「見せる」ことができます。

</details>

### 🚦 モデルルーティング（ローカル意味判定・遅延ゼロ・トークンゼロ）

メッセージが届くと、deepx は**ローカル**でこのターンを flash と pro のどちらで始めるかを決めます。追加の LLM トークンは一切消費しません。判定基準は 2 つだけです：

```
メッセージ > 500 文字                                → pro（モデル不要、オフラインでも有効）
「pro へ昇格」組との最大類似度 ≥ 0.91
        かつ「flash 維持」組との類似度より高い        → pro
それ以外                                            → flash（フォールバック）
```

比較するのは**文全体の意味**であり、キーワードの包含ではありません ——「この 1 行の書き方を整えて」は「最適化」という語が入っていても昇格されず、「デザイン案はどのディレクトリ？」も「デザイン」に引っかかりません。初回起動時に量子化版 multilingual-e5-small をバックグラウンドで取得します（118 MB。取得元は ModelScope → hf-mirror → HuggingFace の順に試行）。**起動をブロックしません**。準備できるまで、あるいはダウンロードに失敗した場合は入口ルーティングを一切行わず、常に flash で開始します。

> `/model flash|pro` で固定した場合はルーティングを迂回し、`auto`（既定）のときだけ上記の判定が走ります。起動モデルはそのターン中固定で、モデルは途中で `SwitchModel` により pro へ昇格できますが降格はしません（モデルを切り替えるとプロンプトキャッシュが全て無効になるため）。

<details>
<summary><b>ルーティングの調整：2 組の例文と 6 つのコマンド</b></summary>

2 つの組は対等で、それぞれ逆方向の誤りを修正します：

| 組                | 内蔵 | 意味                       | 追加するタイミング                             |
| :---------------- | ---: | :------------------------- | :--------------------------------------------- |
| **pro へ昇格**    |   36 | これらに近い → pro で開始  | **取りこぼし**：pro であるべきなのに flash で始まった |
| **flash 維持**    |   22 | これらに近い → flash に戻す | **誤判定**：概念の質問や些細な修正が pro に昇格された |

> 「flash 維持」組は**既にしきい値を超えたが実際は概念の質問や小さな修正であるメッセージを引き戻す**ためのものです。「この組に当たれば flash になる」わけではありません —— どちらの組にも当たらないメッセージは元々 flash です。

| コマンド                                                     | 用途                                             |
| :------------------------------------------------------------ | :----------------------------------------------- |
| `/router-list-pro` `/router-list-flash`                        | 組の現在の例文（番号付き）と実際の判定ルールを表示 |
| `/router-add-pro <文>`                                         | 取りこぼし時                                     |
| `/router-add-flash <文>`                                       | 誤判定時                                         |
| `/router-delete-pro <番号>` `/router-delete-flash <番号>`      | 一覧の番号で削除（各組とも 1 から採番）          |

例文を書く際の 2 原則：

1. **キーワードではなく、完全なタスク文で書く。** ✅ `このサービスのカナリアリリース手順を整理する`；❌ `カナリア リリース 手順` —— 単語の羅列はベクトルが実際のメッセージから遠く、ほとんど何にも一致しません。
2. **具体的なほど安全。** 汎用的すぎる例文は近隣の些事まで巻き込みます（実測：広すぎる例文 1 つが 2 件を救った一方で 2 件を壊しました）。

`~/.deepx/router.yaml` を直接編集することもできます（`patterns` = pro へ昇格、`flash_patterns` = flash 維持）。**次のメッセージから即座に反映され、再起動は不要**です。このファイルは初回起動時に両組の内蔵既定値付きで自動生成されます。編集していない組は、deepx が内蔵表を更新した際に自動同期されます。**編集した組は決して上書きされません**。組を空にする、またはファイルを削除すると内蔵既定値に戻ります。

</details>

### 🧭 コードグラフ（codegraph）

シンボルグラフエンジンを内蔵し、リポジトリ全体の grep やファイルを一つずつ開く代わりに、モデルがシンボル単位のナビゲーション + 呼び出し関係クエリを直接実行できます。

<details>
<summary><b>op 早見表（12 個）</b></summary>

| op             | 用途                       | 必須                       | 説明                                            |
| :------------- | :------------------------- | :------------------------- | :---------------------------------------------- |
| `def`          | シンボルの定義位置          | `name`                    | 関数 / 型 / メソッド / 変数の定義箇所           |
| `refs`         | シンボルの使用箇所          | `name`                    | すべての参照（定義 + 呼び出し + 取得）          |
| `symbols`      | 名前で曖昧検索              | `name`(任意), `kind`(任意) | `kind`: func/method/type/var/const/field        |
| `outline`      | ファイル内のシンボル一覧    | `path`                    | ファイルアウトライン                            |
| `imports`      | ファイルの import 一覧      | `path`                    | 依存の概観                                      |
| `callers`      | 関数の呼び出し元            | `name`                    | **変更時の影響範囲**、Go の暗黙インターフェースも網羅 |
| `callees`      | 関数が呼び出すもの          | `name`                    | 内部フローの理解                                |
| `implementers` | インターフェースの実装者    | `name`                    | Go の暗黙インターフェースを **シンボル精度** で。grep では不可 |
| `subtypes`     | 型を継承 / 埋め込むもの     | `name`                    | サブタイプ追跡                                  |
| `supertypes`   | 型の派生元                  | `name`                    | スーパータイプ / 埋め込みインターフェース       |
| `impact`       | 変更が及ぼす下流            | `name`, `depth`(既定 3)   | 推移閉包、影響範囲分析                          |
| `reindex`      | インデックス強制再構築      | —                          | キャッシュ異常時の手動トリガー                  |

</details>

**対応言語**：Go（stdlib の正確な解析）+ TypeScript / JavaScript / Python / Java / Rust / C / C++ / C# / Ruby / PHP / Kotlin / Swift / Scala / Dart / Vue / Svelte。

**仕組み**：起動時にバックグラウンドの `Prewarm` がインデックスを構築（`loading → ready`）。Write/Update で変更されたファイルは `stale` となり次回クエリで増分再構築。結果は `ファイル:行`（シグネチャ / 呼び出し元付き）で表示しページングされます。

## 🧰 ツール

| 種類        | ツール                             |       plan | auto | review |
| :---------- | :--------------------------------- | ---------: | :--: | :----: |
| 読み取り専用 | `Read` `List` `Tree` `Glob` `Grep` |          ✓ |  ✓   |   ✓    |
| コードグラフ | `CodeGraph`                        |          ✓ |  ✓   |   ✓    |
| ファイル書込 | `Write` `Update`                   |          ✗ |  ✓   |   ⏳   |
| Shell       | `Bash`                             |          ✗ |  ✓   |   ⏳   |
| Web         | `Search` `Fetch`                   |          ✓ |  ✓   |   ✓    |
| メモリ      | `Memory`                           |          ✓ |  ✓   |   ✓    |
| Skill       | `LoadSkill`                        |          ✓ |  ✓   |   ✓    |
| 画像        | `OCR`                              |          ✓ |  ✓   |   ✓    |
| 計画        | `Todo` `CreatePlan`                | LLM が呼び出し |   |        |
| 昇格        | `SwitchModel`                      | LLM が呼び出し |   |        |

> ⏳ = 自動実行されるが人間の確認が必要。

## ⌨️ スラッシュコマンド

| コマンド                             | 動作                                |
| :----------------------------------- | :---------------------------------- |
| `/plan` `/auto` `/review`            | モード切替（読み取り専用 / 自動 / レビュー） |
| `/model`                             | モデル選択ポップアップ（auto=タスク振り分け / flash / pro 固定）；`/model flash` で直接指定も可 |
| `/provider`                          | 設定済みプロバイダ間をすばやく切替：ポップアップで選択（`/provider <名前>` で直接指定も可）。`/config` のたびに設定がプロバイダ名で `~/.deepx/provider.yaml` に保存され、切替時にそのプロバイダの flash/pro を `model.yaml` に書き戻す |
| `/reasoning`                         | `thinking` / `reasoning_effort` をロール毎（flash/pro）に設定するポップアップ；空 = 該当フィールドを送信しない（MiMo など非対応モデルに無影響） |
| `/router-list-pro` `/router-list-flash` `/router-add-pro` `/router-add-flash` `/router-delete-pro` `/router-delete-flash` | ルーティングの 2 組の例文を調整（「🚦 モデルルーティング」参照）：`list` は番号付き一覧と実際のルールを表示、`add <文>` で追加、`delete <番号>` で削除。取りこぼしは `-pro` 組へ、誤判定（概念の質問 / 些細な修正）は `-flash` 組へ。`~/.deepx/router.yaml` の直接編集も可、次のメッセージから有効 |
| `/compact`                           | セッションを手動圧縮                |
| `/new` `/sessions`                   | 新しい会話を開始 / 履歴一覧（↑↓ 選択、Enter で切替） |
| `/status`                            | 右側ステータス欄の表示/非表示（`Ctrl+B` でも可） |
| `/web-config`                        | web パネルのバインド IP とポートをポップアップで設定（「IP [ポート]」を空白区切りで入力;IP 空欄/`127.0.0.1`=ローカルのみ、`0.0.0.0`=LAN でスマホ/タブレットからアクセス可、ポート省略=ランダム）。保存すると再起動なしで即時反映され新しい URL を表示;設定はセッションの `meta.json` に保存、アクセストークンはセッションごとに固定で再起動後も不変。⚠️ このパネルはセッションを操作しコマンドを実行でき、かつ平文 HTTP — 外部公開は信頼できる LAN のみ |
| `/sandbox`                           | サンドボックス：`off`（無効）/ `native`（既定、OS 隔離：macOS は Seatbelt、Linux は bubblewrap — 書き込みを workspace に限定 + プロセス隔離;OS 機構が無い環境ではソフトポリシーのブラックリストにフォールバック)/ `docker`（コンテナ隔離、`/sandbox docker <image>`） |
| `/working-mode`                      | 作業モード（方法論）：`karpathy`（既定、実用重視）/ `openspec`（仕様駆動）/ `superpowers`（全工程を厳格に）；ポップアップで選択、または `/working-mode kp\|spec\|sp` で直接切替。3 モードは排他的で、1 つ選ぶと他 2 つの skill を無効化し方法論の混在を防ぐ。セッションに保存され、毎ターン履歴を汚さずプロンプトを注入 |
| `/ultracode` `/workflow` `/workflows` | Workflow(JS マルチエージェント編成):`/ultracode <説明>` でモデルが生成・保存、`/workflow <名前> [k=v]` で実行(実行前に確認)、`/workflows` で一覧 |
| `/lang`                              | UI 言語切替（中 / 英）              |
| `/mcp-list` `/mcp-add` `/mcp-delete` | MCP サーバー管理                    |
| `/skills` `/config` `/mode`          | skill 一覧 / key 再設定 / モード表示 |
| `/help`                              | ヘルプ                              |
| `/exit`                              | deepx を終了                        |

## 🛡️ レビューモード

| モード             | Write / Update / Bash | その他のツール | コマンド  |
| :----------------- | :-------------------- | :------------- | :-------- |
| `review`（既定）   | 人間が YES/NO 確認    | 自動実行       | `/review` |
| `auto`             | 自動実行              | 自動実行       | `/auto`   |
| `plan`             | 無効                  | 自動実行       | `/plan`   |

## 📦 Skill

```
ワークスペース  <wd>/.deepx/skills/
グローバル      ~/.agents/skills/ → ~/.claude/skills/ → ~/.deepx/skills/
```

- ワークスペース単位は `git add` でチームに共有可能
- グローバルは Claude Code 互換 —— 既存の skill をそのまま再利用

## 🏗️ アーキテクチャ

<details>
<summary><b>データフローを展開</b></summary>

```
1 ターン:
  ユーザー入力
    ↓
  RouteByKeyword (ローカル) ─► flash または pro
    ↓
  StartStream (メインループ)
    ├─ 直接回答
    ├─ ツール呼び出し → review が 書込/Shell をゲート → 実行 → 結果を戻す → 継続
    ├─ Todo → 可視チェックリスト(メインエージェントが一歩ずつ実行)
    ├─ SwitchModel → pro に昇格
    └─ CreatePlan → DAG scheduler → 並列サブエージェント → 集約

永続化:
  HistoryUpdateMsg → SaveGob (history.gob, 完全忠実)
  StreamDoneMsg    → Append JSONL (プレーンテキスト, Memory 検索)
  再起動           → LoadGob (優先) / JSONL (フォールバック)

圧縮:
  tokens ≥ ctxWindow × 70% → runCompression (非同期)
    → 末尾に ~20K トークンを保持 → LLM が新旧サマリを統合 → gob + state.json を更新
```

</details>

**ディレクトリ構成**

```
deepx/
├── main.go
├── agent/      StartStream ツールループ + ルーティング + DAG スケジューラ + サブエージェント
├── config/     ~/.deepx/model.yaml の読み書き
├── session/    gob 永続化 + JSONL ログ + 圧縮状態
├── tools/      全ツール実装（読み書き / 検索 / OCR / Memory / Skill / Plan / CodeGraph）
├── codegraph/  コードグラフ：定義 / 呼び出し / 継承実装 / 影響範囲
├── skill/      複数パスの skill 探索と読み込み
├── ocr/        PaddleOCR ラッパー（ONNX Runtime）
├── tui/        bubbletea TUI（入力 / 描画 / クリップボード / 選択 / ダッシュボード）
└── scripts/    インストールスクリプト
```

## 💰 トークン経済

- **ルーティングはトークンゼロ**：純粋にローカルの文ベクトル比較、LLM 呼び出しなし
- **ツールを事前注入しない**：`Memory` / `LoadSkill` は呼び出し時のみ context に入る
- **system prompt は最小限**：ツール横断の規約 + workspace のみ。トリガー条件は各ツールの description に
- **DeepSeek の KV キャッシュに優しい**：tools 配列はモード / ロールで変わらず、system prompt は gob 復元時にバージョン認識
- **盲目的検索よりコードグラフ**：read / glob / grep のトークン浪費を根本から削減

## 🩹 アンインストール

```bash
# macOS / Linux
rm -f ~/.local/bin/deepx && rm -rf ~/.deepx

# Windows: %LOCALAPPDATA%\Programs\deepx と %USERPROFILE%\.deepx を削除
```

## 📄 License

[MIT](LICENSE) © 2026 itmisx
