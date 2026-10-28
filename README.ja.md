# codespace-template

[English](./README.md)

**自分だけの Dev Container テンプレートを作成・管理・配布するためのツールキット。**

codespace-template は、[codespacegen](https://github.com/taka1156/codespacegen)・GitHub Actions・導入用 Shell Script を組み合わせて Dev Container テンプレートを管理・配布するためのリポジトリです。そのままテンプレートを使うだけでなく、**Fork して自分専用のテンプレート集を構築**できます。ローカルの VS Code と GitHub Codespaces の両方を想定しています。

**テンプレートの利用側で `codespacegen` をインストールする必要はありません。** このリポジトリに配置した Shell Script を実行し、言語を選ぶだけで設定をプロジェクトへ取り込めます。

## 2つの使い方

- **テンプレートを利用する：** Shell Script で言語を選択して導入。利用者側で `codespacegen` のインストールは不要です。
- **自分専用のテンプレート集を作る：** リポジトリを Fork し、生成設定や GitHub Actions をカスタマイズして独自のテンプレートを配布できます。

## クイックスタート

必要なもの：Bash、Git、対話操作ができるターミナル、Dev Container に対応した開発環境。

対象プロジェクトのディレクトリで実行します。

```bash
curl -fsSL https://raw.githubusercontent.com/taka1156/codespace-template/master/scripts/setup_devcontainer.sh | bash
```

言語を指定して実行することもできます。

```bash
curl -fsSL https://raw.githubusercontent.com/taka1156/codespace-template/master/scripts/setup_devcontainer.sh | bash -s -- node
```

> 上記の URL は `master` ブランチの `scripts/setup_devcontainer.sh` にスクリプトを配置する前提です。配置先が異なる場合は変更してください。リモートスクリプトは内容を確認してから実行してください。

スクリプトは以下の処理を行います。

1. Git の sparse checkout で `dist` ブランチの `sample_devcontainer/` を取得。
2. サブディレクトリから利用可能な言語一覧を実行時に自動取得。
3. 言語（引数未指定の場合）とプロジェクト名を対話形式で選択・入力。
4. 選択したテンプレートをコピーし、見つかった `devcontainer.json` の最初の `name` を変更。
5. 配置先に `.devcontainer` が存在する場合は上書きを確認。

デフォルトの配置先はカレントディレクトリです。変更する場合：

```bash
curl -fsSL https://raw.githubusercontent.com/taka1156/codespace-template/master/scripts/setup_devcontainer.sh | DEST_DIR=./my-project bash
```

言語を引数で指定しても、プロジェクト名の入力が必要なため `/dev/tty` を使用します。既存の `.devcontainer` は確認なしで上書きしません。

## 自分専用の Dev Container テンプレート集を作る

1. このリポジトリを GitHub アカウントに **Fork** します。
2. `codespacegen` の設定や生成対象の言語・テンプレートを、自分の技術スタックに合わせて **カスタマイズ**します。
3. GitHub Actions のテンプレート生成・公開、ベースイメージの定期バージョン確認を **確認・設定**します。Workflow を有効化し、必要最小限の権限を設定してください。
4. 生成したテンプレートを Fork 側の `dist` ブランチへ **公開**します（別のブランチを使う場合はスクリプトも変更）。
5. 導入スクリプトの参照リポジトリ・ブランチと、実行用の raw URL を **自分の Fork に変更**します。
6. 自分やチームで Shell Script を実行して、必要なテンプレートを利用します。

Fork は元リポジトリから独立しているため、言語・イメージバージョン・テンプレートを自由に管理できます。Fork 側の変更が元リポジトリに自動反映されることはありません。

## 全体構成

```text
codespace-template（ソースブランチ：master）
├── codespacegen.json            テンプレート生成設定
├── .github/workflows/           GitHub Actions
└── scripts/
    └── setup_devcontainer.sh    利用者向け導入スクリプト
          |
          | GitHub Actions / codespacegen
          v
codespace-template（配布ブランチ：dist）
└── sample_devcontainer/
    ├── <言語A>/
    ├── <言語B>/
    └── ...
          |
          | Git sparse checkout + 言語選択
          v
利用者のプロジェクト
└── .devcontainer/
    ├── devcontainer.json
    └── ... その他の生成ファイル
```

**役割分担：** `codespacegen` は設定を生成し、codespace-template はテンプレートを管理・配布し、Shell Script は選択したテンプレートをプロジェクトへ導入します。

`codespacegen` は Dev Container の設定生成を担当し、このリポジトリは生成済みテンプレートの配布を担当します。GitHub Actions はテンプレートの生成・公開を自動化するほか、ベースイメージのバージョンを定期的に確認します。実際の実行間隔や更新処理は Workflow の設定に従います。

## テンプレートの更新について

配布テンプレートは取得時点のスナップショットです。スクリプトを実行すると、その時点で `dist` ブランチに公開されている内容を取得します。**一度コピーしたプロジェクト内の設定が自動更新されるわけではありません。** カスタマイズ済みの `.devcontainer` がある場合は、再実行前に差分を確認してください。

## 開発環境

導入後は VS Code の Dev Containers 対応環境、または GitHub Codespaces で開発できます。ローカルで利用する場合は Docker など対応するコンテナランタイムが必要です。

## リポジトリ名について

コマンド内の URL は現在の `taka1156/codespace-template` を参照しています。**codespace-template** はプロジェクト名です。GitHub リポジトリ名を変更した場合は URL とスクリプトの参照先も更新してください。

## 関連プロジェクト

- [codespacegen](https://github.com/taka1156/codespacegen) — JSON から Dev Container 設定を生成する CLI。
